# #44: WebDAV upload verification — focused candidate

- Upstream issue: https://github.com/Liquid-co/OpenSave/issues/44
- Source research SHA: `e4c113ff3af3b9556444c1e511e15531f3b529c2`
- Pinned base `dev-public`: `7b690420c0fa03513764f4937af29bebf7f391a7`
- Branch is **fork-only**. It is not an accepted upstream implementation.

## Change

After a successful PUT, OpenSave GETs the remote object and verifies byte length and SHA-256 against the original file, rejecting status, read, timeout, size and digest failures. HEAD is no longer needed for the upload-verification claim. Tests exercise faults and a complete successful round trip.

## Trade-off / review gate

The full remote readback adds object egress, latency and storage-server load for every upload. This is deliberate but requires upstream compatibility and performance review before adoption. It **does not** fix #45: the initial PUT still writes the final archive name and can destroy an existing object before validation discovers a failure. No claim of atomic publication is made.

Before any external PR, review large-object performance and header-based WebDAV compatibility. Never promise remote durability beyond the tested server's guarantees.
