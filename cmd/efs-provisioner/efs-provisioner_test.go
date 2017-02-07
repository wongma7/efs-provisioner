package main

import (
	"path"
	"reflect"
	"testing"

	"k8s.io/client-go/pkg/api/v1"
)

const (
	dnsName    = "fs-47a2c22e.efs.us-west-2.amazonaws.com"
	mountpoint = "/mountpoint/data"
	source     = "/source/data"
)

func TestGetLocalPathToDelete(t *testing.T) {
	tests := []struct {
		name         string
		server       string
		path         string
		expectedPath string
		expectError  bool
	}{
		{
			name:         "pv path has corresponding local path",
			server:       dnsName,
			path:         path.Join(source, "pv/foo/bar"),
			expectedPath: path.Join(mountpoint, "pv/foo/bar"),
		},
		{
			name:        "server is different from provisioner's stored DNS name",
			server:      "foo",
			path:        path.Join(source, "pv"),
			expectError: true,
		},
		{
			name:        "pv path does not have corresponding local path",
			server:      dnsName,
			path:        path.Join("/foo", "pv"),
			expectError: true,
		},
	}
	efsProvisioner := newTestEFSProvisioner()
	for _, test := range tests {
		source := &v1.NFSVolumeSource{
			Server: test.server,
			Path:   test.path,
		}
		path, err := efsProvisioner.getLocalPathToDelete(source)
		evaluate(t, test.name, test.expectError, err, test.expectedPath, path, "local path to delete")
	}
}

func newTestEFSProvisioner() *efsProvisioner {
	return &efsProvisioner{
		dnsName:    dnsName,
		mountpoint: mountpoint,
		source:     source,
	}
}

func evaluate(t *testing.T, name string, expectError bool, err error, expected interface{}, got interface{}, output string) {
	if !expectError && err != nil {
		t.Logf("test case: %s", name)
		t.Errorf("unexpected error getting %s: %v", output, err)
	} else if expectError && err == nil {
		t.Logf("test case: %s", name)
		t.Errorf("expected error but got %s: %v", output, got)
	} else if !reflect.DeepEqual(expected, got) {
		t.Logf("test case: %s", name)
		t.Errorf("expected %s %v but got %s %v", output, expected, output, got)
	}
}
