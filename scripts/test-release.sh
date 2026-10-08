#!/usr/bin/env bash
set -euo pipefail
ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
WORK="$(mktemp -d /tmp/krm-release.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
# Use an isolated source copy so the fixture never touches the production public key.
mkdir -p "$WORK/source"
tar --exclude=.git --exclude=.omx --exclude=dist --exclude=release --exclude=VERSION -cf - . | tar -xf - -C "$WORK/source"
cd "$WORK/source"
# Source archives have no original Git metadata. Tags below identify only this
# disposable snapshot, never the developer checkout or a published release.
unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_INDEX_FILE GIT_OBJECT_DIRECTORY
unset GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_CONFIG GIT_CONFIG_COUNT GIT_CONFIG_PARAMETERS
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME='KRM fixture' GIT_AUTHOR_EMAIL='fixture@invalid.example'
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
mkdir -p "$WORK/git-template"
git init --quiet --initial-branch=fixture --template="$WORK/git-template"
git add --all
git -c commit.gpgsign=false commit --quiet -m 'Disposable release fixture source'
go build -o "$WORK/tool" ./cmd/krm-release-tool
"$WORK/tool" keygen --public "$WORK/public" --private "$WORK/private"
export KRM_RELEASE_PUBLIC_KEY="$WORK/public"
# Exercise exact Git release identity for both channels without a VERSION file.
for fixture_version in 9.9.9-rc.1 9.9.9; do
 if [[ "$fixture_version" == 9.9.9 ]]; then
  git -c commit.gpgsign=false commit --quiet --allow-empty -m 'Disposable stable fixture identity'
 fi
 KRM_RELEASE_TAG="v$fixture_version"
 export KRM_RELEASE_TAG
 git -c tag.gpgSign=false tag "$KRM_RELEASE_TAG"
 [[ "$(python3 scripts/build-version.py --release)" == "$fixture_version" ]]
 KRM_SOURCE_COMMIT=$(git rev-parse HEAD)
 export KRM_SOURCE_COMMIT
 channel=$(python3 scripts/release_channel.py "$fixture_version")
 output="$WORK/output-$channel"
 KRM_RELEASE_CHANNEL="$channel" KRM_RELEASE_PRIVATE_KEY="$WORK/private" OUTPUT_DIR="$output" ./scripts/build-release.sh
 python3 scripts/verify-release.py "$output/dist"
 python3 - "$output/dist" "$fixture_version" "$KRM_SOURCE_COMMIT" "$channel" <<'PYIDENTITY'
import json,pathlib,subprocess,sys
import platform as host_platform
output=pathlib.Path(sys.argv[1]);version,commit,channel=sys.argv[2:]
manifest=json.loads((output/('manifest-'+channel+'.json')).read_text())
assert manifest['version']==version and manifest['channel']==channel
inventory=json.loads((output/'SBOM.spdx.json').read_text())
assert inventory['packages'][0]['versionInfo']==version
assert inventory['documentNamespace'].endswith('/'+version+'/'+commit)
for platform in ('keenetic','openwrt','linux'):
 body=(output/('bootstrap-'+platform+'.sh')).read_text().splitlines()
 assert 'TAG=v'+version in body and 'CHANNEL='+channel in body
for asset in output.iterdir():
 if '-linux-' not in asset.name: continue
 metadata=subprocess.check_output(['go','version','-m',str(asset)],text=True)
 build={line.split('=',1)[0].strip().split()[-1]:line.split('=',1)[1] for line in metadata.splitlines() if line.startswith('\tbuild\t') and '=' in line}
 assert build.get('vcs.revision')==commit and build.get('vcs.modified')=='false', asset.name
# A v9 fixture tag is outside the module's v1 path: Go may record a pseudo-version.
# Check application identity by executing only the explicit read-only version command.
for component in ('kee-route-managerd','kee-route-manager-ui','kee-route-managerctl','kee-route-manager-launcher'):
 if sys.platform.startswith('linux'):
  arch={'x86_64':'amd64','amd64':'amd64','aarch64':'arm64','arm64':'arm64','armv7l':'armv7','mipsle':'mipsle','mipsel':'mipsle'}.get(host_platform.machine())
  if not arch: raise SystemExit('unsupported native fixture architecture')
  command=[str(output/(component+'-linux-'+arch)),'version']
 else:
  command=['docker','run','--rm','--network','none','-v',str(output.resolve())+':/signed:ro','krm-test','sh','-c','case "$(uname -m)" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; armv7l) arch=armv7;; *) exit 2;; esac; exec /signed/"$1"-linux-"$arch" version','fixture',component]
 identity=subprocess.check_output(command,text=True).strip()
 assert identity.startswith(component+' '+version+' ('+commit+', '), identity
print('Verified isolated Git tag, runtime application version/commit, binary VCS identity, SBOM and bootstrap identity.')
PYIDENTITY
 # Change a file: verifier must reject it even though signatures remain valid.
 printf tampered >> "$output/dist/kee-route-manager-ui-linux-amd64"
 if python3 scripts/verify-release.py "$output/dist"; then echo 'tampering was accepted' >&2; exit 1; fi
done
printf 'Both signed release channels and tamper rejection passed.\n'
