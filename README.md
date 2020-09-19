# pvc-remover

Kubernetes controllers to remove a `PersistentVolumeClaim` (PVC) of a particular
`StorageClass` when an associated `Pod` is completed.  

This project's primary purpose is the early deletion of [jobcachefs](https://github.com/mgoltzsche/jobcachefs)
volumes so that the volume contents can be committed and quickly reused
for the next (build) job.  

## How it works

The *Pod controller* annotates and deletes matching PVCs (`storageClassName`, `accessMode=ReadWriteOnce`)
that are associated with a matching, completed Pod (`annotations`?, `labels`?, `status.phase=Succeeded`, `restartPolicy=Never`).  
The *PVC controller* removes the finalizer `kubernetes.io/pvc-protection` from PVCs
that have a deletion timestamp and the annotation `pvc-remover.mgoltzsche.github.com/no-pvc-protection`
allowing unsafe deletion while still referenced by Pods.

## Build

```
make
```

## Test

Run controller tests:
```
make test
```

Test interactively against a real cluster:
```
make manager
./bin/manager --storage-class=<your-ephemeral-storage-class-name>
```
