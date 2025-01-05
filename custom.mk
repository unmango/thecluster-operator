WORKING_DIRECTORY := $(shell pwd)
LOCAL_BIN := ${WORKING_DIRECTORY}/bin

include .versions/*.mk
DEVCTL := ${LOCAL_BIN}/devctl

export GOBIN := ${LOCAL_BIN}

bin/devctl: .versions/devctl
	go install github.com/unmango/devctl/cmd@v${DEVCTL_VERSION}
	mv ${LOCAL_BIN}/cmd $@

bin/kubebuilder: .versions/kubebuilder
	go install sigs.k8s.io/kubebuilder/v4/cmd@v$(shell cat $<)
	mv ${LOCAL_BIN}/cmd $@ && chmod +x $@

.envrc: hack/example.envrc
	cp $< $@
