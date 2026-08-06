//go:build darwin

package runstate

import "syscall"

// localDarwinFilesystems is the known-local allowlist for the probe gate (ADR
// 0015 §1, "Not every flock is this flock"). Membership means: on this
// filesystem, flock(2) is the kernel's own whole-file lock, with the two
// properties the derivation rests on — it conflicts across open file
// descriptions within one process, and closing an unrelated fd on the file
// does not drop it.
//
// The list is deliberately allow-only and deliberately short. Being too strict
// costs precision, never safety: an unlisted filesystem reports
// LivenessUnknown, which is exactly the answer this tool gave before ADR 0015.
// Being too loose is the direction that manufactures a false "abandoned" over
// a network mount.
var localDarwinFilesystems = map[string]bool{
	"apfs": true,
	"hfs":  true,
	"ufs":  true,
}

// isLocalFilesystem reports whether dir sits on a filesystem whose flock(2)
// means what ProbeLock assumes. An error (a missing directory, a permission
// problem) is the caller's cue for LivenessUnknown, never for "not local, so
// carry on".
func isLocalFilesystem(dir string) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return false, err
	}
	name := make([]byte, 0, len(st.Fstypename))
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		name = append(name, byte(c))
	}
	return localDarwinFilesystems[string(name)], nil
}
