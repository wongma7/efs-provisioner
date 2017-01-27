package main

import (
	"flag"
	"os"
	"path"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/efs"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/nfs-provisioner/controller"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	pvDir           = "/persistentvolumes"
	fileSystemIdKey = "FILE_SYSTEM_ID"
	awsRegionKey    = "AWS_REGION"

	resyncPeriod              = 15 * time.Second
	provisionerName           = "kubernetes.io/aws-efs"
	exponentialBackOffOnError = true
	failedRetryThreshold      = 5
)

type efsProvisioner struct {
	// The "root" directory on the file system in which to create dirs for each PV
	pvDir      string
	svc        *efs.EFS
	fileSystem *efs.FileSystemDescription
	awsRegion  string
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

	sess, err := session.NewSession()
	if err != nil {
		glog.Fatal(err)
	}

	svc := efs.New(sess, &aws.Config{Region: aws.String(awsRegion)})

	params := &efs.DescribeFileSystemsInput{
		FileSystemId: aws.String(fileSystemId),
	}

	resp, err := svc.DescribeFileSystems(params)
	if err != nil {
		glog.Fatal(err)
	}

	return &efsProvisioner{
		pvDir:      pvDir,
		svc:        svc,
		fileSystem: resp.FileSystems[0],
		awsRegion:  awsRegion,
	}
}

var _ controller.Provisioner = &efsProvisioner{}

// Provision creates a storage asset and returns a PV object representing it.
func (p *efsProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	path, err := p.createDirectory(options)
	if err != nil {
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
					Server:   p.getMountTarget(),
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

func (p *efsProvisioner) createDirectory(options controller.VolumeOptions) (string, error) {
	path := path.Join(p.pvDir, options.PVC.Name+"-"+options.PVName)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}

	return path, nil
}

func (p *efsProvisioner) getMountTarget() string {
	return *p.fileSystem.FileSystemId + ".efs." + p.awsRegion + ".amazonaws.com"
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
