all: build

build:
	@docker run \
		--rm  \
		-e "CGO_ENABLED=0" \
		-u $$(id -u):$$(id -g) \
		-v "$$(pwd):/go/src/github.com/wongma7/efs-provisioner" \
		-w /go/src/github.com/wongma7/efs-provisioner \
		golang:1.7.4-alpine \
		go build -a -installsuffix cgo

container: build quick-container

quick-container:
	docker build -t docker.io/wongma7/efs-provisioner:latest .

clean:
	rm -f efs-provisioner
