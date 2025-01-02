WORKING_DIRECTORY := $(shell pwd)
LOCAL_BIN := ${WORKING_DIRECTORY}/bin

DEVOPS := ${LOCAL_BIN}/devops

export GOBIN := ${LOCAL_BIN}

bin/devops: .versions/devops
	go install github.com/unmango/go/cmd/devops@v$(shell $(DEVOPS) version devops)

bin/kubebuilder: .versions/kubebuilder
	go install sigs.k8s.io/kubebuilder/v4/cmd@v$(shell $(DEVOPS) version kubebuilder)
	mv bin/cmd $@ && chmod +x $@

.envrc: hack/example.envrc
	cp $< $@
