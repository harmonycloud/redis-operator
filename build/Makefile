docker-build:
	cd exporter&& docker build -t ${registry}/redis-exporter:v1 . &&cd ..
	cd init&& docker build -t ${registry}/redis-init:v1 . &&cd ..
	cd redis&& docker build -t ${registry}/redis-cli-v5-port:v5.0.8 . &&cd ..

docker-push:
	docker push ${registry}/redis-exporter:v1
	docker push ${registry}/redis-init:v1
	docker push ${registry}/redis-cli-v5-port:v5.0.8
