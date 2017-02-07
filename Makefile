# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

all: build

build:
	@mkdir -p .go/src/github.com/wongma7/efs-provisioner
	@mkdir -p .go/bin
	@mkdir -p .go/stdlib
	@docker run \
		--rm  \
		-e "CGO_ENABLED=0" \
		-u $$(id -u):$$(id -g) \
		-v $$(pwd)/.go:/go \
		-v $$(pwd):/go/src/github.com/wongma7/efs-provisioner \
		-v $$(pwd):/go/bin \
		-v $$(pwd)/.go/stdlib:/usr/local/go/pkg/linux_amd64_asdf \
		-w /go/src/github.com/wongma7/efs-provisioner \
		golang:1.7.4-alpine \
		go install -installsuffix "asdf" ./cmd/efs-provisioner

container: build quick-container

quick-container:
	docker build -t docker.io/wongma7/efs-provisioner:latest .

test:
	go test `go list ./... | grep -v 'vendor'`

clean:
	rm -rf .go
	rm -f efs-provisioner
