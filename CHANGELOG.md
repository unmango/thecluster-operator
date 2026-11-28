# Changelog

## [0.1.4](https://github.com/unmango/thecluster-operator/compare/v0.1.3...v0.1.4) (2026-11-28)


### Features

* **harbor:** add robot accounts and a Key Vault secret store writer ([#170](https://github.com/unmango/thecluster-operator/issues/170)) ([fd798f8](https://github.com/unmango/thecluster-operator/commit/fd798f8b353117a62560b36a0c333c4c8082b2b2))

## [0.1.3](https://github.com/unmango/thecluster-operator/compare/v0.1.2...v0.1.3) (2026-11-27)


### Features

* **registry:** add a Project kind for private Harbor projects ([#168](https://github.com/unmango/thecluster-operator/issues/168)) ([9979195](https://github.com/unmango/thecluster-operator/commit/997919562f80cf5ada2b444e7cb2fb174db17ac9))


### Bug Fixes

* **deps:** update k8s.io/utils digest to ca04ba5 ([#152](https://github.com/unmango/thecluster-operator/issues/152)) ([c32c06a](https://github.com/unmango/thecluster-operator/commit/c32c06a304d0bae1b510dfec538be9ea814e31da))


### Documentation

* trim comments to the non-obvious why ([#161](https://github.com/unmango/thecluster-operator/issues/161)) ([f42e5cd](https://github.com/unmango/thecluster-operator/commit/f42e5cd07e692fb0415976baccd768d6917f03c0))


### Code Refactoring

* **harbor:** move the Harbor client to goharbor/go-client ([#167](https://github.com/unmango/thecluster-operator/issues/167)) ([fdbf1b1](https://github.com/unmango/thecluster-operator/commit/fdbf1b10472748701f7169d62948690d4f5769c1)), closes [#162](https://github.com/unmango/thecluster-operator/issues/162)

## [0.1.2](https://github.com/unmango/thecluster-operator/compare/v0.1.1...v0.1.2) (2026-10-26)


### Features

* **actions:** add the Repository CR for runner scale sets ([#141](https://github.com/unmango/thecluster-operator/issues/141)) ([b11d3bf](https://github.com/unmango/thecluster-operator/commit/b11d3bfdab8ecba2edcd4a75caf4ca473e3454a0))
* **actions:** add the User CR that generates Repositories ([#147](https://github.com/unmango/thecluster-operator/issues/147)) ([5b41528](https://github.com/unmango/thecluster-operator/commit/5b41528afc03bc2b2f5b16a792a27b1bc8e43bcd))


### Bug Fixes

* **deps:** update module github.com/onsi/gomega to v1.44.0 ([#143](https://github.com/unmango/thecluster-operator/issues/143)) ([7f2da24](https://github.com/unmango/thecluster-operator/commit/7f2da246cb70317e9f95600c7af621165d85496d))


### Tests

* **e2e:** install the AutoscalingRunnerSet CRD before deploying ([#151](https://github.com/unmango/thecluster-operator/issues/151)) ([f0f7f08](https://github.com/unmango/thecluster-operator/commit/f0f7f089350582ca3facdb19392bac8c591e9282))

## [0.1.1](https://github.com/unmango/thecluster-operator/compare/v0.1.0...v0.1.1) (2026-10-09)


### Features

* **registry:** Registry and ProxyCache CRs with a Harbor backend ([#139](https://github.com/unmango/thecluster-operator/issues/139)) ([f8da0a7](https://github.com/unmango/thecluster-operator/commit/f8da0a73994a046291387532fd0dcddab869acc0))


### Documentation

* add Hercules CI badge ([#118](https://github.com/unmango/thecluster-operator/issues/118)) ([0886053](https://github.com/unmango/thecluster-operator/commit/088605355cba46749251298ace0856c131dab550))


### Build System

* modernize tooling, CI, and dependencies ([#138](https://github.com/unmango/thecluster-operator/issues/138)) ([9c0861b](https://github.com/unmango/thecluster-operator/commit/9c0861b2826b76fd74ebaf7479e90af2862a87a7))
