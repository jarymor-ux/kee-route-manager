#!/bin/sh
# Shared native ELF check; embedded verbatim in generated signed bootstraps.
# Optional arguments exist for fixture testing, not environment overrides.
krm_detect_arch(){
 krm_machine=${1:-$(uname -m)}
 krm_elf=${2:-/bin/sh}
 case "$krm_machine" in
  x86_64|amd64) printf 'amd64\n'; return;;
  aarch64|arm64) printf 'arm64\n'; return;;
  armv7l|armv7*) printf 'armv7\n'; return;;
  mips|mipsel|mipsle) ;;
  *) echo 'ERROR: unsupported CPU architecture' >&2; return 1;;
 esac
 # uname frequently reports just mips for both byte orders. Inspect the
 # running userspace shell, not the spelling or /proc/cpuinfo CPU marketing name.
 krm_header=$(od -An -v -t u1 -N 40 "$krm_elf") || return 1
 set -- $krm_header
 [ "$#" = 40 ] || { echo 'ERROR: truncated native ELF header' >&2; return 1; }
 [ "$1 $2 $3 $4 $5" = '127 69 76 70 1' ] || { echo 'ERROR: MIPS requires 32-bit ELF O32 userspace' >&2; return 1; }
 krm_endian=$6
 shift 18
 krm_machine_low=$1; krm_machine_high=$2
 case "$krm_endian:$krm_machine_low:$krm_machine_high" in
  1:8:0) krm_arch=mipsle;;
  2:0:8) krm_arch=mips;;
  *) echo 'ERROR: invalid native MIPS ELF machine/endianness' >&2; return 1;;
 esac
 case "$krm_machine:$krm_arch" in
  mipsel:mips|mipsle:mips) echo 'ERROR: uname and native ELF byte order disagree' >&2; return 1;;
 esac
 shift 18
 if [ "$krm_endian" = 1 ]; then krm_flags_low=$1; krm_flags_abi=$2; else krm_flags_low=$4; krm_flags_abi=$3; fi
 # Reject N32 (EF_MIPS_ABI2), EABI, O64 and other non-O32 encodings.
 # Historical O32 ELF can omit the ABI mask entirely.
 if [ "$((krm_flags_low & 32))" != 0 ] || { [ "$((krm_flags_abi & 240))" != 0 ] && [ "$((krm_flags_abi & 240))" != 16 ]; }; then
  echo 'ERROR: unsupported native MIPS ABI; Linux O32 is required' >&2; return 1
 fi
 printf '%s\n' "$krm_arch"
}
