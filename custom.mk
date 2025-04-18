DEVCTL      := go tool devctl
KUBEBUILDER := go tool kubebuilder
KIND        := go tool kind

tidy: go.sum

kind-cluster: hack/kind-config.yml
	$(KIND) create cluster --config $<

go.sum: go.mod $(shell $(DEVCTL) list --go)
	go mod tidy

.envrc: hack/example.envrc
	cp $< $@
