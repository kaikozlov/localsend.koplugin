// Fault-injection launcher for the QEMU legacy-ARM compatibility test.
//
// The Kindle 5.6.1.1 ARM syscall table (see docs/LEGACY_KERNEL_COMPATIBILITY.md)
// returns ENOSYS for several calls that a modern host kernel implements. QEMU
// user mode forwards guest syscalls to the host, so this launcher installs a
// seccomp filter on the host syscalls that correspond to those gaps.
//
// Filter policy (host syscall numbers):
//   accept4        ENOSYS when flags != 0
//   epoll_create1  ENOSYS
//   eventfd2       ENOSYS when flags != 0
//   pipe2          ENOSYS when flags != 0
//   dup3           ENOSYS
//   recvmmsg       ENOSYS
//   sendmmsg       ENOSYS
//   getrandom      ENOSYS
//   prlimit64      ENOSYS
//
// The flags==0 exceptions are required by QEMU's host mappings. QEMU maps
// guest legacy accept, eventfd, and pipe onto host accept4, eventfd2, and
// pipe2 respectively, with flags 0. Modern guest calls use nonzero CLOEXEC
// and NONBLOCK flags, so the filter can reject them without breaking the
// legacy fallback.
//
// epoll_pwait is intentionally NOT filtered here. QEMU user mode implements
// guest epoll_wait by calling host epoll_pwait, so blocking the host syscall
// also kills the patched overlay path that issues guest epoll_wait (ARM 252).
// Verify epoll_wait vs epoll_pwait with qemu-arm -strace (guest numbers) and
// by objdump of the overlay-built binary instead.
#define _GNU_SOURCE

#include <errno.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <stddef.h>
#include <stdio.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>

int main(int argc, char **argv) {
	if (argc < 2) {
		fprintf(stderr, "usage: %s PROGRAM [ARG...]\n", argv[0]);
		return 2;
	}

	struct sock_filter code[] = {
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS,
			 offsetof(struct seccomp_data, nr)),

		/* accept4: ENOSYS unless flags (arg 3) is 0 */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_accept4, 0, 4),
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS,
			 offsetof(struct seccomp_data, args[3])),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 0, 1, 0),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),

		/* epoll_create1: the legacy epoll_create host call is distinct. */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_epoll_create1, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),

		/* eventfd2: permit QEMU's flags==0 mapping for guest eventfd. */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_eventfd2, 0, 4),
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS,
			 offsetof(struct seccomp_data, args[1])),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 0, 1, 0),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),

		/* pipe2: permit QEMU's flags==0 mapping for guest pipe. */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_pipe2, 0, 4),
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS,
			 offsetof(struct seccomp_data, args[1])),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 0, 1, 0),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),

		/* dup3 is distinct from the legacy dup2 host call. */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_dup3, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),

		/* Unconditional Kindle-shaped gaps that QEMU forwards 1:1. */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_recvmmsg, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),

		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_sendmmsg, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),

		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_getrandom, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),

		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_prlimit64, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),

		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
	};
	struct sock_fprog program = {
		.len = (unsigned short)(sizeof(code) / sizeof(code[0])),
		.filter = code,
	};

	if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) < 0) {
		perror("PR_SET_NO_NEW_PRIVS");
		return 1;
	}
	if (prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &program) < 0) {
		perror("PR_SET_SECCOMP");
		return 1;
	}

	execvp(argv[1], &argv[1]);
	perror("execvp");
	return 1;
}
