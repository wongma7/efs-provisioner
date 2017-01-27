package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/efs"
	"github.com/docker/docker/pkg/mount"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/nfs-provisioner/controller"
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
	// The "" directory on the file system in which to create dirs for each PV
	// fs-042be7ad.efs.us-west-2.amazonaws.com://persistentvolumes on /persistentvolumes
	dnsName    string
	mountpoint string
	source     string
	svc        *efs.EFS
}

func NewEFSProvisioner() controller.Provisioner {
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
	if err := os.MkdirAll(p.getLocalPath(options), 0755); err != nil {
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: options.PVName,
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
	path := volume.Spec.NFS.Path
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
	efsProvisioner := NewEFSProvisioner()

	// Start the provision controller which will dynamically provision efs NFS
	// PVs
	pc := controller.NewProvisionController(clientset, resyncPeriod, provisionerName, efsProvisioner, serverVersion.GitVersion, exponentialBackOffOnError, failedRetryThreshold)
	pc.Run(wait.NeverStop)
}
