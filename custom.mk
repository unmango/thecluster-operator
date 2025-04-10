WORKING_DIRECTORY := $(shell pwd)
LOCAL_BIN := ${WORKING_DIRECTORY}/bin

include .versions/*.mk

DEVCTL      := go tool devctl
KUBEBUILDER := go tool kubebuilder

tidy: go.sum

go.sum: go.mod $(shell $(DEVCTL) list --go)
	go mod tidy

.envrc: hack/example.envrc
	cp $< $@
