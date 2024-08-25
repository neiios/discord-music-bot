redeploy:
	git pull
	docker compose up --build --force-recreate -d
