DEVCTL      := go tool devctl
KUBEBUILDER := go tool kubebuilder

tidy: go.sum

# The context kind writes names no namespace. Inside a pod, such as a
# thecluster runner, kubectl then falls back to the pod's own namespace rather
# than default, and the e2e specs that omit -n land somewhere that does not
# exist in the kind cluster.
kind-cluster: hack/kind-config.yml
	$(KIND) create cluster --config $<
	$(KUBECTL) config set-context --current --namespace=default

go.sum: go.mod $(shell $(DEVCTL) list --go)
	go mod tidy

.envrc: hack/example.envrc
	cp $< $@
