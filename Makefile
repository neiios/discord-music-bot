deploy:
	git pull
	podman compose build --no-cache
	podman compose up --force-recreate -d
