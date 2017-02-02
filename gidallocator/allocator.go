package gidallocator

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/nfs-provisioner/controller"
	"github.com/wongma7/efs-provisioner/allocator"
	"github.com/wongma7/efs-provisioner/util"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	defaultGidMin = 2000
	defaultGidMax = math.MaxInt32
	// absoluteGidMin/Max are currently the same as the
	// default values, but they play a different role and
	// could take a different value. Only thing we need is:
	// absGidMin <= defGidMin <= defGidMax <= absGidMax
	absoluteGidMin = 2000
	absoluteGidMax = math.MaxInt32
)

type Allocator struct {
	client       kubernetes.Interface
	gidTable     map[string]*allocator.MinMaxAllocator
	gidTableLock sync.Mutex
}

func New(client kubernetes.Interface) Allocator {
	return Allocator{
		client:   client,
		gidTable: make(map[string]*allocator.MinMaxAllocator),
	}
}

func (a *Allocator) AllocateNext(options controller.VolumeOptions) (int, error) {
	class := util.GetClaimStorageClass(options.PVC)
	gidMin, gidMax, err := parseClassParameters(options.Parameters)
	if err != nil {
		return 0, err
	}

	gidTable, err := a.getGidTable(class, gidMin, gidMax)
	if err != nil {
		return 0, fmt.Errorf("failed to get gidTable: %v", err)
	}

	gid, _, err := gidTable.AllocateNext()
	if err != nil {
		return 0, fmt.Errorf("failed to reserve gid from table: %v", err)
	}

	return gid, nil
}

func (a *Allocator) Release(volume *v1.PersistentVolume) error {
	class, err := util.GetClassForVolume(a.client, volume)
	if err != nil {
		return err
	}
	gidMin, gidMax, err := parseClassParameters(class.Parameters)
	if err != nil {
		return err
	}

	gid, exists, err := getGid(volume)
	if err != nil {
		glog.Error(err)
	} else if exists {
		gidTable, err := a.getGidTable(class.Name, gidMin, gidMax)
		if err != nil {
			return fmt.Errorf("failed to get gidTable: %v", err)
		}

		err = gidTable.Release(gid)
		if err != nil {
			return fmt.Errorf("failed to release gid %v: %v", gid, err)
		}
	}

	return nil
}

//
// Return the gid table for a storage class.
// - If this is the first time, fill it with all the gids
//   used in PVs of this storage class by traversing the PVs.
// - Adapt the range of the table to the current range of the SC.
//
func (a *Allocator) getGidTable(className string, min int, max int) (*allocator.MinMaxAllocator, error) {
	var err error
	a.gidTableLock.Lock()
	gidTable, ok := a.gidTable[className]
	a.gidTableLock.Unlock()

	if ok {
		err = gidTable.SetRange(min, max)
		if err != nil {
			return nil, err
		}

		return gidTable, nil
	}

	// create a new table and fill it
	newGidTable, err := allocator.NewMinMaxAllocator(0, absoluteGidMax)
	if err != nil {
		return nil, err
	}

	// collect gids with the full range
	err = a.collectGids(className, newGidTable)
	if err != nil {
		return nil, err
	}

	// and only reduce the range afterwards
	err = newGidTable.SetRange(min, max)
	if err != nil {
		return nil, err
	}

	// if in the meantime a table appeared, use it

	a.gidTableLock.Lock()
	defer a.gidTableLock.Unlock()

	gidTable, ok = a.gidTable[className]
	if ok {
		err = gidTable.SetRange(min, max)
		if err != nil {
			return nil, err
		}

		return gidTable, nil
	}

	a.gidTable[className] = newGidTable

	return newGidTable, nil
}

// Traverse the PVs, fetching all the GIDs from those
// in a given storage class, and mark them in the table.
//
func (a *Allocator) collectGids(className string, gidTable *allocator.MinMaxAllocator) error {
	pvList, err := a.client.Core().PersistentVolumes().List(v1.ListOptions{})
	if err != nil {
		glog.Errorf("failed to get existing persistent volumes")
		return err
	}

	for _, pv := range pvList.Items {
		if util.GetVolumeStorageClass(&pv) != className {
			continue
		}

		pvName := pv.ObjectMeta.Name

		gidStr, ok := pv.Annotations[util.VolumeGidAnnotationKey]

		if !ok {
			glog.Warningf("no gid found in pv '%v'", pvName)
			continue
		}

		gid, err := convertGid(gidStr)
		if err != nil {
			glog.Error(err)
			continue
		}

		_, err = gidTable.Allocate(gid)
		if err == allocator.ErrConflict {
			glog.Warningf("gid %v found in pv %v was already allocated", gid)
		} else if err != nil {
			glog.Errorf("failed to store gid %v found in pv '%v': %v", gid, pvName, err)
			return err
		}
	}

	return nil
}

func parseClassParameters(params map[string]string) (int, int, error) {
	gidMin := defaultGidMin
	gidMax := defaultGidMax

	for k, v := range params {
		switch strings.ToLower(k) {
		case "gidmin":
			parseGidMin, err := convertGid(v)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid value %s for parameter %s: %v", v, k, err)
			}
			if parseGidMin < absoluteGidMin {
				return 0, 0, fmt.Errorf("gidmin must be >= %v", absoluteGidMin)
			}
			if parseGidMin > absoluteGidMax {
				return 0, 0, fmt.Errorf("gidmin must be <= %v", absoluteGidMax)
			}
			gidMin = parseGidMin
		case "gidmax":
			parseGidMax, err := convertGid(v)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid value %s for parameter %s: %v", v, k, err)
			}
			if parseGidMax < absoluteGidMin {
				return 0, 0, fmt.Errorf("gidmax must be >= %v", absoluteGidMin)
			}
			if parseGidMax > absoluteGidMax {
				return 0, 0, fmt.Errorf("gidmax must be <= %v", absoluteGidMax)
			}
			gidMax = parseGidMax
		}
	}

	if gidMin > gidMax {
		return 0, 0, fmt.Errorf("gidmax %v is not >= gidmin %v", gidMax, gidMin)
	}

	return gidMin, gidMax, nil
}

func getGid(volume *v1.PersistentVolume) (int, bool, error) {
	gidStr, ok := volume.Annotations[util.VolumeGidAnnotationKey]

	if !ok {
		return 0, false, nil
	}

	gid, err := convertGid(gidStr)

	return gid, true, err
}

func convertGid(gidString string) (int, error) {
	gid64, err := strconv.ParseInt(gidString, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("failed to parse gid %v ", gidString)
	}

	if gid64 < 0 {
		return 0, fmt.Errorf("negative GIDs are not allowed: %v", gidString)
	}

	// ParseInt returns a int64, but since we parsed only
	// for 32 bit, we can cast to int without loss:
	gid := int(gid64)
	return gid, nil
}
