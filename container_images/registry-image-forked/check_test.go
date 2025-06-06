package resource_test

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	resource "github.com/GoogleCloudPlatform/guest-test-infra/container_images/registry-image-forked"
)

var _ = Describe("Check", func() {
	var actualErr error

	var req struct {
		Source  resource.Source
		Version *resource.Version
	}

	var res []resource.Version

	BeforeEach(func() {
		req.Source = resource.Source{}
		req.Version = nil

		res = nil
	})

	check := func() {
		cmd := exec.Command(bins.Check)
		cmd.Env = []string{"TEST=true"}

		payload, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		outBuf := new(bytes.Buffer)

		cmd.Stdin = bytes.NewBuffer(payload)
		cmd.Stdout = outBuf
		cmd.Stderr = GinkgoWriter

		actualErr = cmd.Run()
		if actualErr == nil {
			err = json.Unmarshal(outBuf.Bytes(), &res)
			Expect(err).ToNot(HaveOccurred())
		}
	}

	Describe("tracking a single tag", func() {
		JustBeforeEach(check)

		Context("when invoked with no cursor version", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: "concourse/test-image-static",
					Tag:        "latest",
				}

				req.Version = nil
			})

			It("returns the current digest", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res).To(Equal([]resource.Version{
					{Tag: "latest", Digest: LATEST_STATIC_DIGEST},
				}))
			})

			Context("against a private repo with credentials", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: dockerPrivateRepo,
						Tag:        "latest",

						BasicCredentials: resource.BasicCredentials{
							Username: dockerPrivateUsername,
							Password: dockerPrivatePassword,
						},
					}

					checkDockerPrivateUserConfigured()
				})

				It("returns the current digest", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{
						{Tag: "latest", Digest: PRIVATE_LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("when the registry does not return Docker-Content-Digest", func() {
				var registry *ghttp.Server

				BeforeEach(func() {
					registry = ghttp.NewServer()
				})

				AfterEach(func() {
					registry.Close()
				})

				BeforeEach(func() {
					registry.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/"),
							ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("HEAD", "/v2/fake-image/manifests/latest"),
							ghttp.RespondWith(http.StatusOK, ``, http.Header{
								"Content-Length": LATEST_FAKE_HEADERS["Content-Length"],
							}),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/fake-image/manifests/latest"),
							ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`, http.Header{
								"Content-Length": LATEST_FAKE_HEADERS["Content-Length"],
							}),
						),
					)

					req.Source.Repository = registry.Addr() + "/fake-image"
				})

				It("falls back on fetching the manifest", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{
						{Tag: "latest", Digest: LATEST_FAKE_DIGEST},
					}))
				})
			})

			Context("using a registry with self-signed certificate", func() {
				var registry *ghttp.Server

				BeforeEach(func() {
					registry = ghttp.NewTLSServer()

					registry.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/"),
							ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("HEAD", "/v2/some/fake-image/manifests/latest"),
							ghttp.RespondWith(http.StatusOK, ``, LATEST_FAKE_HEADERS),
						),
					)

					req.Source.Repository = registry.Addr() + "/some/fake-image"
				})

				AfterEach(func() {
					registry.Close()
				})

				When("the certificate is provided in 'source'", func() {
					BeforeEach(func() {
						certPem := pem.EncodeToMemory(&pem.Block{
							Type:  "CERTIFICATE",
							Bytes: registry.HTTPTestServer.Certificate().Raw,
						})
						Expect(certPem).ToNot(BeEmpty())

						req.Source.DomainCerts = []string{string(certPem)}
					})

					It("it checks and returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "latest", Digest: LATEST_FAKE_DIGEST},
						}))
					})
				})

				When("the certificate is missing in 'source'", func() {
					It("exits non-zero and returns an error", func() {
						Expect(actualErr).To(HaveOccurred())
					})
				})
			})

			Context("against a mirror", func() {
				var mirror *ghttp.Server

				BeforeEach(func() {
					mirror = ghttp.NewServer()

					req.Source.RegistryMirror = &resource.RegistryMirror{
						Host: mirror.Addr(),
					}
				})

				AfterEach(func() {
					mirror.Close()
				})

				Context("when the repository contains a registry host name prefixed image", func() {
					var registry *ghttp.Server

					BeforeEach(func() {
						registry = ghttp.NewServer()

						registry.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/some/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, ``, LATEST_FAKE_HEADERS),
							),
						)

						req.Source.Repository = registry.Addr() + "/some/fake-image"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: mirror.Addr(),
						}
					})

					It("it checks and returns the current digest using the registry declared in the repository and not using the mirror", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "latest", Digest: LATEST_FAKE_DIGEST},
						}))

						Expect(mirror.ReceivedRequests()).To(BeEmpty())
					})
				})

				Context("which has the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, ``, LATEST_FAKE_HEADERS),
							),
						)

						req.Source.Repository = "fake-image"
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "latest", Digest: LATEST_FAKE_DIGEST},
						}))
					})
				})

				Context("which is missing the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.Tag = "1.32.0"
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "1.32.0", Digest: latestDigest(req.Source.Name())},
						}))
					})
				})
			})
		})

		Context("when invoked with an up-to-date cursor version", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: "concourse/test-image-static",
					Tag:        "latest",
				}

				req.Version = &resource.Version{
					Tag:    "latest",
					Digest: LATEST_STATIC_DIGEST,
				}
			})

			It("returns the given digest", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res).To(Equal([]resource.Version{
					{Tag: "latest", Digest: LATEST_STATIC_DIGEST},
				}))
			})

			Context("when the cursor version is missing the tag", func() {
				BeforeEach(func() {
					req.Version.Tag = ""
				})

				It("includes the tag in the response version", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{
						{Tag: "latest", Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("against a private repo with credentials", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: dockerPrivateRepo,
						Tag:        "latest",

						BasicCredentials: resource.BasicCredentials{
							Username: dockerPrivateUsername,
							Password: dockerPrivatePassword,
						},
					}

					checkDockerPrivateUserConfigured()

					req.Version = &resource.Version{
						Tag:    "latest",
						Digest: PRIVATE_LATEST_STATIC_DIGEST,
					}
				})

				It("returns the current digest", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{
						{Tag: "latest", Digest: PRIVATE_LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("against a mirror", func() {
				var mirror *ghttp.Server

				BeforeEach(func() {
					mirror = ghttp.NewServer()

					req.Source.RegistryMirror = &resource.RegistryMirror{
						Host: mirror.Addr(),
					}
				})

				AfterEach(func() {
					mirror.Close()
				})

				Context("which has the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, ``, LATEST_FAKE_HEADERS),
							),
						)

						req.Source.Repository = "fake-image"

						req.Version = &resource.Version{
							Tag:    "latest",
							Digest: LATEST_FAKE_DIGEST,
						}
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							*req.Version,
						}))
					})
				})

				Context("which is missing the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.Tag = "1.32.0"

						req.Version = &resource.Version{
							Tag:    "1.32.0",
							Digest: latestDigest(req.Source.Name()),
						}
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							*req.Version,
						}))
					})
				})
			})
		})

		Context("when invoked with a valid but out-of-date cursor version", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: "concourse/test-image-static",
					Tag:        "latest",
				}

				req.Version = &resource.Version{
					// this was previously pushed to the 'latest' tag
					Tag:    "latest",
					Digest: OLDER_STATIC_DIGEST,
				}
			})

			It("returns the previous digest and the current digest", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res).To(Equal([]resource.Version{
					{Tag: "latest", Digest: OLDER_STATIC_DIGEST},
					{Tag: "latest", Digest: LATEST_STATIC_DIGEST},
				}))
			})

			Context("against a private repo with credentials", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: dockerPrivateRepo,
						Tag:        "latest",

						BasicCredentials: resource.BasicCredentials{
							Username: dockerPrivateUsername,
							Password: dockerPrivatePassword,
						},
					}

					checkDockerPrivateUserConfigured()

					req.Version = &resource.Version{
						// this was previously pushed to the 'latest' tag
						Tag:    "latest",
						Digest: PRIVATE_OLDER_STATIC_DIGEST,
					}
				})

				It("returns the current digest", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{
						{Tag: "latest", Digest: PRIVATE_OLDER_STATIC_DIGEST},
						{Tag: "latest", Digest: PRIVATE_LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("against a mirror", func() {
				var mirror *ghttp.Server

				BeforeEach(func() {
					mirror = ghttp.NewServer()

					req.Source.RegistryMirror = &resource.RegistryMirror{
						Host: mirror.Addr(),
					}
				})

				AfterEach(func() {
					mirror.Close()
				})

				Context("which has the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, ``, LATEST_FAKE_HEADERS),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/"+OLDER_FAKE_DIGEST),
								ghttp.RespondWith(http.StatusOK, ``, OLDER_FAKE_HEADERS),
							),
						)

						req.Source.Repository = "fake-image"

						req.Version.Digest = OLDER_FAKE_DIGEST
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "latest", Digest: OLDER_FAKE_DIGEST},
							{Tag: "latest", Digest: LATEST_FAKE_DIGEST},
						}))
					})
				})

				Context("which is missing the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.Tag = "1.32.0"

						req.Version.Tag = "1.32.0"
						req.Version.Digest = OLDER_LIBRARY_DIGEST
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "1.32.0", Digest: OLDER_LIBRARY_DIGEST},
							{Tag: "1.32.0", Digest: latestDigest(req.Source.Name())},
						}))
					})
				})
			})
		})

		Context("when invoked with an invalid cursor version", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: "concourse/test-image-static",
					Tag:        "latest",
				}

				req.Version = &resource.Version{
					Tag:    "latest",
					Digest: "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				}
			})

			It("returns only the current digest", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res).To(Equal([]resource.Version{
					{Tag: "latest", Digest: LATEST_STATIC_DIGEST},
				}))
			})

			Context("against a private repo with credentials", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: dockerPrivateRepo,
						Tag:        "latest",

						BasicCredentials: resource.BasicCredentials{
							Username: dockerPrivateUsername,
							Password: dockerPrivatePassword,
						},
					}

					checkDockerPrivateUserConfigured()
				})

				It("returns the current digest", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{
						{Tag: "latest", Digest: PRIVATE_LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("against a mirror", func() {
				var mirror *ghttp.Server

				BeforeEach(func() {
					mirror = ghttp.NewServer()

					req.Source.RegistryMirror = &resource.RegistryMirror{
						Host: mirror.Addr(),
					}
				})

				AfterEach(func() {
					mirror.Close()
				})

				Context("which has the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, ``, LATEST_FAKE_HEADERS),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/"+req.Version.Digest),
								ghttp.RespondWith(http.StatusNotFound, `{"errors":[{"code": "MANIFEST_UNKNOWN", "message": "ruh roh", "detail": "not here"}]}`),
							),
						)

						req.Source.Repository = "fake-image"
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "latest", Digest: LATEST_FAKE_DIGEST},
						}))
					})
				})

				Context("which is missing the image", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.Tag = "1.32.0"
					})

					It("returns the current digest", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						Expect(res).To(Equal([]resource.Version{
							{Tag: "1.32.0", Digest: latestDigest(req.Source.Name())},
						}))
					})
				})
			})
		})

		Context("when invoked with not exist image", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: "concourse/test-image-static",
					Tag:        "not-exist-image",
				}
				req.Version = nil
			})

			It("returns empty digest", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res).To(Equal([]resource.Version{}))
			})

			Context("against a private repo with credentials", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: dockerPrivateRepo,
						Tag:        "not-exist-image",

						BasicCredentials: resource.BasicCredentials{
							Username: dockerPrivateUsername,
							Password: dockerPrivatePassword,
						},
					}

					checkDockerPrivateUserConfigured()
				})

				It("returns empty digest", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res).To(Equal([]resource.Version{}))
				})
			})
		})
	})
})

var _ = DescribeTable("tracking semver tags",
	(SemverTagCheckExample).Run,
	Entry("no semver tags",
		SemverTagCheckExample{
			Tags: map[string]string{
				"non-semver-tag": "random-1",
			},
			Versions: []string{},
		},
	),
	Entry("latest tag",
		SemverTagCheckExample{
			Tags: map[string]string{
				"non-semver-tag": "random-1",
				"latest":         "random-2",
			},
			Versions: []string{"latest"},
		},
	),
	Entry("HEAD with GET fallback",
		SemverTagCheckExample{
			Tags: map[string]string{
				"non-semver-tag": "random-1",
				"latest":         "random-2",
			},
			NoHEAD:   true,
			Versions: []string{"latest"},
		},
	),
	Entry("semver and non-semver tags",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0":          "random-1",
				"non-semver-tag": "random-2",
			},
			Versions: []string{"1.0.0"},
		},
	),
	Entry("semver tag ordering",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},
			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("semver tag ordering with cursor",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},
			From: &resource.Version{
				Tag:    "1.2.1",
				Digest: "random-3",
			},
			Versions: []string{"1.2.1", "2.0.0"},
		},
	),
	Entry("semver tag ordering with cursor with different digest",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},
			From: &resource.Version{
				Tag:    "1.2.1",
				Digest: "bogus",
			},
			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("semver constraint",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"1.2.2": "random-4",
				"2.0.0": "random-5",
				// Does not include bare tag
				"latest": "random-6",
			},
			SemverConstraint: "1.2.x",
			Versions:         []string{"1.2.1", "1.2.2"},
		},
	),
	Entry("prereleases ignored by default",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-alpha.1": "random-0",
				"1.0.0":         "random-1",
				"1.2.1-beta.1":  "random-2",
				"1.2.1":         "random-3",
				"2.0.0-rc.1":    "random-4",
				"2.0.0":         "random-5",
			},
			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("prereleases opted in",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-alpha.1": "random-0",
				"1.0.0":         "random-1",
				"1.2.1-beta.1":  "random-2",
				"1.2.1":         "random-3",
				"2.0.0-rc.1":    "random-4",
				"2.0.0":         "random-5",
			},
			PreReleases: true,
			Versions: []string{
				"1.0.0-alpha.1",
				"1.0.0",
				"1.2.1-beta.1",
				"1.2.1",
				"2.0.0-rc.1",
				"2.0.0",
			},
		},
	),
	Entry("prereleases do not include 'variants'",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-alpha.1": "random-0",
				"1.0.0-beta.1":  "random-1",
				"1.0.0-rc.1":    "random-2",
				"1.0.0-foo.1":   "random-3",
			},
			PreReleases: true,
			Versions: []string{
				"1.0.0-alpha.1",
				"1.0.0-beta.1",
				"1.0.0-rc.1",
			},
		},
	),
	Entry("prereleases do not require dot",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-alpha1": "random-0",
				"1.0.0-alpha2": "random-1",
				"1.0.0-beta1":  "random-2",
				"1.0.0-beta2":  "random-3",
				"1.0.0-rc1":    "random-4",
				"1.0.0-rc2":    "random-5",
				"1.0.0-foo1":   "random-6",
				"1.0.0-foo2":   "random-7",
			},
			PreReleases: true,
			Versions: []string{
				"1.0.0-alpha1",
				"1.0.0-alpha2",
				"1.0.0-beta1",
				"1.0.0-beta2",
				"1.0.0-rc1",
				"1.0.0-rc2",
			},
		},
	),
	Entry("prereleases do not require number",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-alpha": "random-0",
				"1.0.0-beta":  "random-1",
				"1.0.0-rc":    "random-2",
				"1.0.0-foo":   "random-3",
			},
			PreReleases: true,
			Versions: []string{
				"1.0.0-alpha",
				"1.0.0-beta",
				"1.0.0-rc",
			},
		},
	),
	Entry("final versions take priority over rcs",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-rc.1": "random-2",
				"1.0.0-rc1":  "random-2",
				"1.0.0-rc":   "random-2",
				"1.0.0":      "random-2",
			},
			PreReleases: true,
			Versions:    []string{"1.0.0"},
		},
	),
	Entry("mixed specificity semver tags",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1":      "random-1",
				"2":      "random-2",
				"2.1":    "random-2",
				"latest": "random-3",
				"3":      "random-3",
				"3.2":    "random-3",
				"3.2.1":  "random-3",
				"3.1":    "random-4",
				"3.1.0":  "random-4",
				"3.0":    "random-5",
				"3.0.0":  "random-5",
			},
			Versions: []string{"1", "2.1", "3.0.0", "3.1.0", "3.2.1"},
		},
	),
	Entry("semver tags with latest tag having unique digest",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0":          "random-1",
				"non-semver-tag": "random-2",
				"latest":         "random-3",
			},
			Versions: []string{"1.0.0", "latest"},
		},
	),
	Entry("latest tag pointing to latest version",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1":      "random-1",
				"2":      "random-2",
				"3":      "random-3",
				"latest": "random-3",
			},
			Versions: []string{"1", "2", "3"},
		},
	),
	Entry("latest tag pointing to older version",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1":      "random-1",
				"2":      "random-2",
				"latest": "random-2",
				"3":      "random-3",
			},
			Versions: []string{"1", "2", "3"},
		},
	),
	Entry("variants",
		SemverTagCheckExample{
			Tags: map[string]string{
				"latest":    "random-1",
				"1.0.0":     "random-1",
				"0.9.0":     "random-2",
				"foo":       "random-3",
				"1.0.0-foo": "random-3",
				"0.9.0-foo": "random-4",
				"bar":       "random-5",
				"1.0.0-bar": "random-5",
				"0.9.0-bar": "random-6",
			},

			Variant: "foo",

			Versions: []string{"0.9.0-foo", "1.0.0-foo"},
		},
	),
	Entry("variant with bare variant tag pointing to unique digest",
		SemverTagCheckExample{
			Tags: map[string]string{
				"latest":    "random-1",
				"1.0.0":     "random-1",
				"0.9.0":     "random-2",
				"foo":       "random-3",
				"0.8.0-foo": "random-4",
				"bar":       "random-5",
				"1.0.0-bar": "random-5",
				"0.9.0-bar": "random-6",
			},

			Variant: "foo",

			Versions: []string{"0.8.0-foo", "foo"},
		},
	),
	Entry("distinguishing additional variants from prereleases",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0-foo":             "random-1",
				"1.0.0-rc.1-foo":        "random-2",
				"1.0.0-alpha.1-foo":     "random-3",
				"1.0.0-beta.1-foo":      "random-4",
				"1.0.0-bar-foo":         "random-5",
				"1.0.0-rc.1-bar-foo":    "random-6",
				"1.0.0-alpha.1-bar-foo": "random-7",
				"1.0.0-beta.1-bar-foo":  "random-8",
			},

			Variant:     "foo",
			PreReleases: true,

			Versions: []string{
				"1.0.0-alpha.1-foo",
				"1.0.0-beta.1-foo",
				"1.0.0-rc.1-foo",
				"1.0.0-foo",
			},
		},
	),
	Entry("tries mirror and falls back on original repository",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},

			RegistryMirror: "fakeserver.foo:5000",

			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("uses mirror and ignores failing repository",
		SemverTagCheckExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},

			Repository:    "test-image",
			WorkingMirror: true,

			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
)

type SemverTagCheckExample struct {
	Tags map[string]string

	PreReleases bool
	Variant     string

	SemverConstraint string

	Repository     string
	RegistryMirror string
	WorkingMirror  bool

	From *resource.Version

	Versions []string

	NoHEAD bool
}

func (example SemverTagCheckExample) Run() {
	registryServer := ghttp.NewServer()
	defer registryServer.Close()

	registryServer.RouteToHandler(
		"GET",
		"/v2/",
		ghttp.RespondWith(http.StatusOK, ""),
	)

	repoStr := fmt.Sprintf("%s/test-image", registryServer.Addr())
	if example.Repository != "" {
		repoStr = example.Repository
	}

	var err error
	repo, err := name.NewRepository(repoStr)
	Expect(err).ToNot(HaveOccurred())

	req := resource.CheckRequest{
		Source: resource.Source{
			Repository:       repo.Name(),
			PreReleases:      example.PreReleases,
			Variant:          example.Variant,
			SemverConstraint: example.SemverConstraint,
		},
	}

	if example.RegistryMirror != "" {
		req.Source.RegistryMirror = &resource.RegistryMirror{
			Host: example.RegistryMirror,
		}
	} else if example.WorkingMirror {
		req.Source.RegistryMirror = &resource.RegistryMirror{
			Host: registryServer.Addr(),
		}
	}

	tagNames := []string{}
	for name := range example.Tags {
		tagNames = append(tagNames, name)
	}

	registryServer.RouteToHandler(
		"GET",
		"/v2/"+repo.RepositoryStr()+"/tags/list",
		ghttp.RespondWithJSONEncoded(http.StatusOK, registryTagsResponse{
			Name: "some-name",
			Tags: tagNames,
		}),
	)

	images := map[string]v1.Image{}

	tagVersions := map[string]resource.Version{}
	for name, imageName := range example.Tags {
		image, found := images[imageName]
		if !found {
			var err error
			image, err = random.Image(1024, 1)
			Expect(err).ToNot(HaveOccurred())

			images[imageName] = image
		}

		manifest, err := image.RawManifest()
		Expect(err).ToNot(HaveOccurred())

		mediaType, err := image.MediaType()
		Expect(err).ToNot(HaveOccurred())

		digest, err := image.Digest()
		Expect(err).ToNot(HaveOccurred())

		if example.NoHEAD {
			registryServer.RouteToHandler(
				"HEAD",
				"/v2/"+repo.RepositoryStr()+"/manifests/"+name,
				ghttp.RespondWith(http.StatusOK, manifest, http.Header{
					"Content-Type":   {string(mediaType)},
					"Content-Length": {strconv.Itoa(len(manifest))},
				}),
			)
			registryServer.RouteToHandler(
				"GET",
				"/v2/"+repo.RepositoryStr()+"/manifests/"+name,
				ghttp.RespondWith(http.StatusOK, manifest, http.Header{
					"Content-Type":   {string(mediaType)},
					"Content-Length": {strconv.Itoa(len(manifest))},
				}),
			)
		} else {
			registryServer.RouteToHandler(
				"HEAD",
				"/v2/"+repo.RepositoryStr()+"/manifests/"+name,
				ghttp.RespondWith(http.StatusOK, manifest, http.Header{
					"Content-Type":          {string(mediaType)},
					"Content-Length":        {strconv.Itoa(len(manifest))},
					"Docker-Content-Digest": {digest.String()},
				}),
			)
		}

		tagVersions[name] = resource.Version{
			Tag:    name,
			Digest: digest.String(),
		}
	}

	if example.From != nil {
		req.Version = &resource.Version{
			Tag: example.From.Tag,
		}

		image, found := images[example.From.Digest]
		if found {
			digest, err := image.Digest()
			Expect(err).ToNot(HaveOccurred())

			req.Version.Digest = digest.String()
		} else {
			// intentionally bogus digest
			req.Version.Digest = example.From.Digest
		}
	}

	res := example.check(req)

	expectedVersions := make(resource.CheckResponse, len(example.Versions))
	for i, ver := range example.Versions {
		expectedVersions[i] = tagVersions[ver]
	}

	Expect(res).To(Equal(expectedVersions))
}

func (example SemverTagCheckExample) check(req resource.CheckRequest) resource.CheckResponse {
	cmd := exec.Command(bins.Check)
	cmd.Env = []string{"TEST=true"}

	payload, err := json.Marshal(req)
	Expect(err).ToNot(HaveOccurred())

	outBuf := new(bytes.Buffer)

	cmd.Stdin = bytes.NewBuffer(payload)
	cmd.Stdout = outBuf
	cmd.Stderr = GinkgoWriter

	err = cmd.Run()
	Expect(err).ToNot(HaveOccurred())

	var res resource.CheckResponse
	err = json.Unmarshal(outBuf.Bytes(), &res)
	Expect(err).ToNot(HaveOccurred())

	return res
}
