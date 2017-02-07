# efs-provisioner

```console
$ kubectl create secret generic aws-credentials \
--from-literal=aws-access-key-id=AKIAIOSFODNN7EXAMPLE \
--from-literal=aws-secret-access-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

```console
$ kubectl create configmap efs-provisioner \
--from-literal=file.system.id=fs-47a2c22e \
--from-literal=aws.region=us-west-2
```

```console
$ kubectl create -f deploy/deployment.yaml
deployment "efs-provisioner" created
```

```console
$ kubectl create -f deploy/class.yaml 
storageclass "aws-efs" created
```

```console
$ kubectl create -f deploy/claim.yaml 
persistentvolumeclaim "efs" created
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM         REASON    AGE
pvc-557b4436-ed73-11e6-84b3-06a700dda5f5   1Mi        RWX           Delete          Bound     default/efs             2s
```
