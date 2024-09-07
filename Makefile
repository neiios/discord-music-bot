redeploy:
	git pull
	podman compose up --build --force-recreate -d
