//go:build linux

package runstate

import "syscall"

// localLinuxFilesystems is the known-local allowlist for the probe gate, keyed
// by the f_type magic statfs(2) reports (ADR 0015 §1, "Not every flock is this
// flock"). It exists because on linux flock() over NFS is emulated as
// whole-file POSIX record locks — per-process, and dropped when any fd on the
// file is closed — which is precisely the design ADR 0015 rejects, and which
// fails silently rather than returning an error.
//
// Allow-only and deliberately short: an unlisted filesystem reports
// LivenessUnknown, which is the pre-ADR-0015 answer, so over-strictness costs
// precision and never safety. overlayfs and tmpfs are listed because
// containers and CI runners are ordinary homes for a run directory, not exotic
// ones.
var localLinuxFilesystems = map[uint32]bool{
	0xEF53:     true, // ext2/ext3/ext4 (one magic for the family)
	0x9123683E: true, // btrfs
	0x58465342: true, // xfs
	0x01021994: true, // tmpfs
	0x794C7630: true, // overlayfs
	0x2FC12FC1: true, // zfs
	0xF2F52010: true, // f2fs
	0x858458F6: true, // ramfs
	0xCA451A4E: true, // bcachefs
}

// isLocalFilesystem reports whether dir sits on a filesystem whose flock(2)
// means what ProbeLock assumes. An error (a missing directory, a permission
// problem) is the caller's cue for LivenessUnknown, never for "not local, so
// carry on".
//
// Statfs_t.Type is int64 on 64-bit and int32 on 32-bit linux, while every
// magic here is 32 bits, so the comparison is made in uint32 — where
// 0x9123683E is the same number on both.
func isLocalFilesystem(dir string) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return false, err
	}
	return localLinuxFilesystems[uint32(st.Type)], nil
}
