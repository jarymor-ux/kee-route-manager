import pathlib,subprocess,sys
name=sys.argv[1]
source=pathlib.Path('/tmp/krm-fix-evidence/container-source-path.txt').read_text().strip()
commands={'linux-runtime':'./scripts/test-runtime.sh','linux-install':'./scripts/test-install.sh','linux-nft':'KRM_TEST_REAL_NFT=1 go test -count=1 -v ./internal/platform -run ^TestRealNft'}
argv=['docker','run','--rm','--network','none','--mount','type=bind,source='+source+',target=/checkout,readonly','--entrypoint','bash']
if name.removesuffix('-verified')=='linux-nft':argv+=['--cap-add','NET_ADMIN']
argv+=['sha256:521fa0c1ab8c6f27a1c2194e1ba79d96b1914d760b9c61ffd0a0bc4e47814e14','-c','cp -a /checkout /tmp/source && cd /tmp/source && '+commands[name.removesuffix('-verified')]]
sys.exit(subprocess.call([sys.executable,'/tmp/krm-fix-evidence/run.py',name]+argv))
