.PHONY: all docker
all: docker

../go-base/Dockerfile:
	(cd ..; git clone https://github.com/dgl/go-base)

docker: Dockerfile
	DOCKER_BUILDKIT=1 docker build .

docker-debug: Dockerfile
	DOCKER_BUILDKIT=1 docker build --build-arg BUILD_DEBUG=1 --progress plain .

Dockerfile: ../go-base/Dockerfile Dockerfile.tail
	cat $^ > $@

wasted: *.go *.txt *.html go.mod go.sum
	CGO_ENABLED=0 go build
