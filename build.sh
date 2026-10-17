#!/bin/sh
# Build the proofreader from the single source file.
#
#   ./build.sh               restart via syscall.Exec (Unix)
#   ./build.sh --no-restart  restart disabled (portable; this is what build.bat
#                            compiles, because Windows has no syscall.Exec)
#
# With no flag this builds the restart-enabled binary (the default); --no-restart
# is the portable variant used for CI and by build.bat.
#
# --no-restart compiles a transformed *temporary copy*; proofreader.go is never
# modified. Set OUT to choose the output path (default: ./proofreader).
set -eu
cd "$(dirname "$0")"

out="${OUT:-proofreader}"

usage() {
	echo "usage: $0 [--no-restart]"
	echo "  (no flag)     restart via syscall.Exec (default)"
	echo "  --no-restart  restart disabled (portable; this is what build.bat uses)"
	echo "  OUT=path      output file (default: ./proofreader)"
}

case "${1:-}" in
-h | --help)
	usage
	exit 0
	;;
--no-restart)
	tmp="${TMPDIR:-/tmp}/proofreader_variant.$$.go"
	trap 'rm -f "$tmp"' EXIT INT TERM
	# Replace the Unix-only call so the file compiles on every platform, and
	# disable the restart setup so the button is shown as unavailable.
	sed -e '/^[[:space:]]*"syscall"[[:space:]]*$/d' \
	    -e 's|^[[:space:]]*return syscall\.Exec(cmd\[0\], cmd, os\.Environ())[[:space:]]*$|c := exec.Command(cmd[0], cmd[1:]...); c.Env = os.Environ(); c.Stdout = os.Stdout; c.Stderr = os.Stderr; if err := c.Start(); err != nil { return err }; os.Exit(0); return nil|' \
	    -e 's|^[[:space:]]*src := findGoSource()[[:space:]]*$|src := findGoSource(); src = ``|' \
	    proofreader.go > "$tmp"
	go build -o "$out" "$tmp"
	echo "Built $out (restart disabled)"
	;;
"")
	go build -o "$out" proofreader.go
	echo "Built $out (restart: syscall.Exec)"
	;;
*)
	usage >&2
	exit 2
	;;
esac
