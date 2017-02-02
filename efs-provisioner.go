package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/efs"
	"github.com/docker/docker/pkg/mount"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/nfs-provisioner/controller"
	"github.com/wongma7/efs-provisioner/gidallocator"
	"github.com/wongma7/efs-provisioner/util"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	fileSystemIdKey = "FILE_SYSTEM_ID"
	awsRegionKey    = "AWS_REGION"

	resyncPeriod              = 15 * time.Second
	provisionerName           = "foobar.io/aws-efs"
	exponentialBackOffOnError = true
	failedRetryThreshold      = 5
)

type efsProvisioner struct {
	dnsName    string
	mountpoint string
	source     string
	svc        *efs.EFS
	allocator  gidallocator.Allocator
}

func NewEFSProvisioner(client kubernetes.Interface) controller.Provisioner {
	fileSystemId := os.Getenv(fileSystemIdKey)
	if fileSystemId == "" {
		glog.Fatal("environment variable %s is not set! Please set it.", fileSystemIdKey)
	}

	awsRegion := os.Getenv(awsRegionKey)
	if awsRegion == "" {
		glog.Fatal("environment variable %s is not set! Please set it.", awsRegionKey)
	}

	dnsName := getDNSName(fileSystemId, awsRegion)

	mountpoint, source, err := getMount(dnsName)
	if err != nil {
		glog.Fatal(err)
	}

	sess, err := session.NewSession()
	if err != nil {
		glog.Fatal(err)
	}

	svc := efs.New(sess, &aws.Config{Region: aws.String(awsRegion)})
	params := &efs.DescribeFileSystemsInput{
		FileSystemId: aws.String(fileSystemId),
	}

	_, err = svc.DescribeFileSystems(params)
	if err != nil {
		glog.Fatal(err)
	}

	return &efsProvisioner{
		dnsName:    dnsName,
		mountpoint: mountpoint,
		source:     source,
		svc:        svc,
		allocator:  gidallocator.New(client),
	}
}

func getDNSName(fileSystemId, awsRegion string) string {
	return fileSystemId + ".efs." + awsRegion + ".amazonaws.com"
}

func getMount(dnsName string) (string, string, error) {
	entries, err := mount.GetMounts()
	if err != nil {
		return "", "", err
	}
	for _, e := range entries {
		glog.Errorf("entry %v\n", e)
		if strings.HasPrefix(e.Source, dnsName) {
			return e.Mountpoint, e.Source, nil
		}
	}

	return "", "", fmt.Errorf("No mount entry found for %s", dnsName)
}

var _ controller.Provisioner = &efsProvisioner{}

// Provision creates a storage asset and returns a PV object representing it.
func (p *efsProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim.Spec.Selector is not supported")
	}

	gid, err := p.allocator.AllocateNext(options)
	if err != nil {
		return nil, err
	}

	err = p.createVolume(p.getLocalPath(options), gid)
	if err != nil {
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				util.VolumeGidAnnotationKey: strconv.FormatInt(int64(gid), 10),
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   p.dnsName,
					Path:     p.getRemotePath(options),
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

func (p *efsProvisioner) createVolume(path string, gid int) error {
	perm := os.FileMode(0071)

	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}

	// Due to umask, need to chmod
	cmd := exec.Command("chmod", strconv.FormatInt(int64(perm), 8), path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(path)
		return fmt.Errorf("chmod failed with error: %v, output: %s", err, out)
	}

	cmd = exec.Command("chgrp", strconv.Itoa(gid), path)
	out, err = cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(path)
		return fmt.Errorf("chgrp failed with error: %v, output: %s", err, out)
	}

	return nil
}

func (p *efsProvisioner) getLocalPath(options controller.VolumeOptions) string {
	return path.Join(p.mountpoint, p.getDirectoryName(options))
}

func (p *efsProvisioner) getRemotePath(options controller.VolumeOptions) string {
	sourcePath := path.Clean(strings.Replace(p.source, p.dnsName+":", "", 1))
	return path.Join(sourcePath, p.getDirectoryName(options))
}

func (p *efsProvisioner) getDirectoryName(options controller.VolumeOptions) string {
	return options.PVC.Name + "-" + options.PVName
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *efsProvisioner) Delete(volume *v1.PersistentVolume) error {
	err := p.allocator.Release(volume)
	if err != nil {
		return err
	}

	path := volume.Spec.NFS.Path
	// TODO this is the remote path *NOT* the local path.
	if err := os.RemoveAll(path); err != nil {
		return err
	}

	return nil
}

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes
	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	efsProvisioner := NewEFSProvisioner(clientset)

	// Start the provision controller which will dynamically provision efs NFS
	// PVs
	pc := controller.NewProvisionController(clientset, resyncPeriod, provisionerName, efsProvisioner, serverVersion.GitVersion, exponentialBackOffOnError, failedRetryThreshold)
	pc.Run(wait.NeverStop)
}
