# efs-provisioner


## Deployment

Create a configmap containing the [**File system ID**](http://docs.aws.amazon.com/efs/latest/ug/gs-step-two-create-efs-resources.html) and Amazon EC2 region of the EFS file system you wish to provision NFS PVs from, plus the name of the provisioner, which administrators will specify in the `provisioner` field of their `StorageClass(es)`, e.g. `provisioner: foobar.io/aws-efs`.

```console
$ kubectl create configmap efs-provisioner \
--from-literal=file.system.id=fs-47a2c22e \
--from-literal=aws.region=us-west-2 \
--from-literal=provisioner.name=foobar.io/aws-efs
```

Create a secret containing AWS credentials for the provisioner to use. The credentials will be used only once at startup to check that the EFS file system you specified in the configmap actually exists.

```console
$ kubectl create secret generic aws-credentials \
--from-literal=aws-access-key-id=AKIAIOSFODNN7EXAMPLE \
--from-literal=aws-secret-access-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

Decide on & set aside a directory within the EFS file system for the provisioner to use. The provisioner will create child directories to back each PV it provisions. Then edit the `volumes` section at the bottom of "deploy/deployment.yaml" so that the `path` refers to the directory you set aside and the `server` is the same EFS file system you specified. Create the deployment, and you're done.

```yaml
      volumes:
        - name: pv-volume
          nfs:
            server: fs-47a2c22e.efs.us-west-2.amazonaws.com
            path: /persistentvolumes
```

```console
$ kubectl create -f deploy/deployment.yaml
deployment "efs-provisioner" created
```

## Usage

First a [`StorageClass`](https://kubernetes.io/docs/user-guide/persistent-volumes/#storageclasses) for claims to ask for needs to be created.

```yaml
apiVersion: storage.k8s.io/v1beta1
kind: StorageClass
metadata:
  name: slow
provisioner: foobar.io/aws-efs
parameters:
  gidMin: "40000"
  gidMax: "50000"
```

## Parameters

* `gidMin` + `gidMax` : The minimum and maximum value of GID range for the storage class. A unique value (GID) in this range ( gidMin-gidMax ) will be used for dynamically provisioned volumes. These are optional values. If not specified, the volume will be provisioned with a value between 2000-2147483647 which are defaults for gidMin and gidMax respectively.

Once you have finished configuring the class to have the name you chose when deploying the provisioner and the parameters you want, create it.

```console
$ kubectl create -f deploy/class.yaml 
storageclass "aws-efs" created
```

When you create a claim that asks for the class, a volume will be automatically created.

```console
$ kubectl create -f deploy/claim.yaml 
persistentvolumeclaim "efs" created
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM         REASON    AGE
pvc-557b4436-ed73-11e6-84b3-06a700dda5f5   1Mi        RWX           Delete          Bound     default/efs             2s
```
