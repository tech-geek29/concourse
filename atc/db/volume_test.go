package db_test

import (
	"time"

	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/dbtest"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Volume", func() {
	var defaultCreatingContainer db.CreatingContainer
	var defaultCreatedContainer db.CreatedContainer

	BeforeEach(func() {
		expiries := db.ContainerOwnerExpiries{
			Min: 5 * time.Minute,
			Max: 1 * time.Hour,
		}

		resourceConfig, err := resourceConfigFactory.FindOrCreateResourceConfig("some-base-resource-type", atc.Source{}, atc.VersionedResourceTypes{})
		Expect(err).ToNot(HaveOccurred())

		defaultCreatingContainer, err = defaultWorker.CreateContainer(
			db.NewResourceConfigCheckSessionContainerOwner(
				resourceConfig.ID(),
				resourceConfig.OriginBaseResourceType().ID,
				expiries,
			),
			db.ContainerMetadata{Type: "check"},
		)
		Expect(err).ToNot(HaveOccurred())

		defaultCreatedContainer, err = defaultCreatingContainer.Created()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("creatingVolume.Failed", func() {
		var (
			creatingVolume db.CreatingVolume
			failedVolume   db.FailedVolume
			failErr        error
		)

		BeforeEach(func() {
			var err error
			creatingVolume, err = volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
			failedVolume, failErr = creatingVolume.Failed()
		})

		Describe("the database query fails", func() {
			Context("when the volume is not in creating or failed state", func() {
				BeforeEach(func() {
					_, err := creatingVolume.Created()
					Expect(err).ToNot(HaveOccurred())
				})

				It("returns the correct error", func() {
					Expect(failErr).To(HaveOccurred())
					Expect(failErr).To(Equal(db.ErrVolumeMarkStateFailed{db.VolumeStateFailed}))
				})
			})

			Context("there is no such id in the table", func() {
				BeforeEach(func() {
					createdVol, err := creatingVolume.Created()
					Expect(err).ToNot(HaveOccurred())

					destroyingVol, err := createdVol.Destroying()
					Expect(err).ToNot(HaveOccurred())

					deleted, err := destroyingVol.Destroy()
					Expect(err).ToNot(HaveOccurred())
					Expect(deleted).To(BeTrue())
				})

				It("returns the correct error", func() {
					Expect(failErr).To(HaveOccurred())
					Expect(failErr).To(Equal(db.ErrVolumeMarkStateFailed{db.VolumeStateFailed}))
				})
			})
		})

		Describe("the database query succeeds", func() {
			Context("when the volume is already in the failed state", func() {
				BeforeEach(func() {
					_, err := creatingVolume.Failed()
					Expect(err).ToNot(HaveOccurred())
				})

				It("returns the failed volume", func() {
					Expect(failedVolume).ToNot(BeNil())
				})

				It("does not fail to transition", func() {
					Expect(failErr).ToNot(HaveOccurred())
				})
			})
		})
	})

	Describe("creatingVolume.Created", func() {
		var (
			creatingVolume db.CreatingVolume
			createdVolume  db.CreatedVolume
			createErr      error
		)

		BeforeEach(func() {
			var err error
			creatingVolume, err = volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
			createdVolume, createErr = creatingVolume.Created()
		})

		Describe("the database query fails", func() {
			Context("when the volume is not in creating or created state", func() {
				BeforeEach(func() {
					createdVolume, err := creatingVolume.Created()
					Expect(err).ToNot(HaveOccurred())
					_, err = createdVolume.Destroying()
					Expect(err).ToNot(HaveOccurred())
				})

				It("returns the correct error", func() {
					Expect(createErr).To(HaveOccurred())
					Expect(createErr).To(Equal(db.ErrVolumeMarkCreatedFailed{Handle: creatingVolume.Handle()}))
				})
			})

			Context("there is no such id in the table", func() {
				BeforeEach(func() {
					vc, err := creatingVolume.Created()
					Expect(err).ToNot(HaveOccurred())

					vd, err := vc.Destroying()
					Expect(err).ToNot(HaveOccurred())

					deleted, err := vd.Destroy()
					Expect(err).ToNot(HaveOccurred())
					Expect(deleted).To(BeTrue())
				})

				It("returns the correct error", func() {
					Expect(createErr).To(HaveOccurred())
					Expect(createErr).To(Equal(db.ErrVolumeMarkCreatedFailed{Handle: creatingVolume.Handle()}))
				})
			})
		})

		Describe("the database query succeeds", func() {
			It("updates the record to be `created`", func() {
				foundVolumes, err := volumeRepository.FindVolumesForContainer(defaultCreatedContainer)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundVolumes).To(ContainElement(WithTransform(db.CreatedVolume.Path, Equal("/path/to/volume"))))
			})

			It("returns a createdVolume and no error", func() {
				Expect(createdVolume).ToNot(BeNil())
				Expect(createErr).ToNot(HaveOccurred())
			})

			Context("when volume is already in provided state", func() {
				BeforeEach(func() {
					_, err := creatingVolume.Created()
					Expect(err).ToNot(HaveOccurred())
				})

				It("returns a createdVolume and no error", func() {
					Expect(createdVolume).ToNot(BeNil())
					Expect(createErr).ToNot(HaveOccurred())
				})
			})
		})
	})

	Describe("createdVolume.InitializeResourceCache", func() {
		var createdVolume db.CreatedVolume
		var resourceCache db.UsedResourceCache
		var build db.Build
		var scenario *dbtest.Scenario

		volumeOnWorker := func(worker db.Worker) db.CreatedVolume {
			creatingContainer, err := worker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", scenario.Team.ID()), db.ContainerMetadata{
				Type:     "get",
				StepName: "some-resource",
			})
			Expect(err).ToNot(HaveOccurred())

			creatingVolume, err := volumeRepository.CreateContainerVolume(scenario.Team.ID(), worker.Name(), creatingContainer, "some-path")
			Expect(err).ToNot(HaveOccurred())

			createdVolume, err := creatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			return createdVolume
		}

		BeforeEach(func() {
			scenario = dbtest.Setup(
				builder.WithTeam("some-team"),
				builder.WithBaseWorker(),
			)

			var err error
			build, err = scenario.Team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			resourceCache, err = resourceCacheFactory.FindOrCreateResourceCache(
				db.ForBuild(build.ID()),
				"some-type",
				atc.Version{"some": "version"},
				atc.Source{
					"some": "source",
				},
				atc.Params{"some": "params"},
				atc.VersionedResourceTypes{
					atc.VersionedResourceType{
						ResourceType: atc.ResourceType{
							Name: "some-type",
							Type: dbtest.BaseResourceType,
							Source: atc.Source{
								"some-type": "source",
							},
						},
						Version: atc.Version{"some-type": "version"},
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())

			createdVolume = volumeOnWorker(scenario.Workers[0])
			err = createdVolume.InitializeResourceCache(resourceCache)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when initialize created resource cache", func() {
			It("should find the worker resource cache", func() {
				_, found, err := db.WorkerResourceCache{
					WorkerName:    scenario.Workers[0].Name(),
					ResourceCache: resourceCache,
				}.Find(dbConn)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
			})

			It("associates the volume to the resource cache", func() {
				foundVolume, found, err := volumeRepository.FindResourceCacheVolume(scenario.Workers[0].Name(), resourceCache)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundVolume.Handle()).To(Equal(createdVolume.Handle()))
				Expect(found).To(BeTrue())
			})

			Context("when there's already an initialized resource cache on the same worker", func() {
				It("leaves the volume owned by the container", func() {
					createdVolume2 := volumeOnWorker(scenario.Workers[0])
					err := createdVolume2.InitializeResourceCache(resourceCache)
					Expect(err).ToNot(HaveOccurred())
					Expect(createdVolume2.Type()).To(Equal(db.VolumeTypeContainer))
				})
			})
		})

		Context("when the same resource cache is initialized from another source worker", func() {
			It("leaves the volume owned by the container", func() {
				scenario.Run(builder.WithBaseWorker())
				worker2CacheVolume := volumeOnWorker(scenario.Workers[1])
				err := worker2CacheVolume.InitializeResourceCache(resourceCache)
				Expect(err).ToNot(HaveOccurred())

				worker1Volume := volumeOnWorker(scenario.Workers[0])
				err = worker1Volume.InitializeStreamedResourceCache(resourceCache, scenario.Workers[1].Name())
				Expect(err).ToNot(HaveOccurred())

				Expect(worker1Volume.Type()).To(Equal(db.VolumeTypeContainer))
			})
		})

		Context("when initialize streamed resource cache", func() {
			var streamedVolume1 db.CreatedVolume

			BeforeEach(func() {
				scenario.Run(builder.WithBaseWorker())

				streamedVolume1 = volumeOnWorker(scenario.Workers[1])
				err := streamedVolume1.InitializeStreamedResourceCache(resourceCache, scenario.Workers[0].Name())
				Expect(err).ToNot(HaveOccurred())
			})

			It("should find the worker resource cache", func() {
				_, found, err := db.WorkerResourceCache{
					WorkerName:    scenario.Workers[1].Name(),
					ResourceCache: resourceCache,
				}.Find(dbConn)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
			})

			It("associates the volume to the resource cache", func() {
				foundVolume, found, err := volumeRepository.FindResourceCacheVolume(scenario.Workers[1].Name(), resourceCache)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(foundVolume.Handle()).To(Equal(streamedVolume1.Handle()))
			})

			Context("when a streamed resource cache is streamed to another worker", func() {
				BeforeEach(func() {
					scenario.Run(builder.WithBaseWorker())

					streamedVolume2 := volumeOnWorker(scenario.Workers[2])
					err := streamedVolume2.InitializeStreamedResourceCache(resourceCache, scenario.Workers[1].Name())
					Expect(err).ToNot(HaveOccurred())
				})

				It("should find the worker resource cache", func() {
					_, found, err := db.WorkerResourceCache{
						WorkerName:    scenario.Workers[2].Name(),
						ResourceCache: resourceCache,
					}.Find(dbConn)
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())
				})

				It("should be invalidated when the original base resource type is invalidated", func() {
					scenario.Run(
						builder.WithWorker(atc.Worker{
							Name:          scenario.Workers[0].Name(),
							ResourceTypes: []atc.WorkerResourceType{
								// empty => invalidate the existing worker_base_resource_type
							},
						}),
					)

					_, found, err := volumeRepository.FindResourceCacheVolume(scenario.Workers[0].Name(), resourceCache)
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeFalse())

					_, found, err = volumeRepository.FindResourceCacheVolume(scenario.Workers[1].Name(), resourceCache)
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeFalse())

					_, found, err = volumeRepository.FindResourceCacheVolume(scenario.Workers[2].Name(), resourceCache)
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeFalse())
				})
			})
		})

		Context("when streaming a volume cache that has been invalidated on the source worker", func() {
			It("leaves the volume owned by the container", func() {
				scenario.Run(
					builder.WithBaseWorker(), // workers[1]
				)

				cachedVolume := volumeOnWorker(scenario.Workers[0])
				err := cachedVolume.InitializeResourceCache(resourceCache)
				Expect(err).ToNot(HaveOccurred())

				scenario.Run(
					builder.WithWorker(atc.Worker{
						Name:          scenario.Workers[0].Name(),
						ResourceTypes: []atc.WorkerResourceType{
							// empty => invalidate the existing worker_resource_cache
						},
					}),
				)

				streamedVolume := volumeOnWorker(scenario.Workers[1])
				err = streamedVolume.InitializeStreamedResourceCache(resourceCache, scenario.Workers[0].Name())
				Expect(err).ToNot(HaveOccurred())

				Expect(streamedVolume.Type()).To(Equal(db.VolumeTypeContainer))
			})
		})
	})

	Describe("createdVolume.InitializeArtifact", func() {
		var (
			workerArtifact db.WorkerArtifact
			creatingVolume db.CreatingVolume
			createdVolume  db.CreatedVolume
			err            error
		)

		BeforeEach(func() {
			creatingVolume, err = volumeRepository.CreateVolume(defaultTeam.ID(), defaultWorker.Name(), db.VolumeTypeArtifact)
			Expect(err).ToNot(HaveOccurred())

			createdVolume, err = creatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
			workerArtifact, err = createdVolume.InitializeArtifact("some-name", 0)
			Expect(err).ToNot(HaveOccurred())
		})

		It("initializes the worker artifact", func() {
			Expect(workerArtifact.ID()).To(Equal(1))
			Expect(workerArtifact.Name()).To(Equal("some-name"))
			Expect(workerArtifact.BuildID()).To(Equal(0))
			Expect(workerArtifact.CreatedAt()).ToNot(BeNil())
		})

		It("associates worker artifact with the volume", func() {
			created, found, err := volumeRepository.FindVolume(createdVolume.Handle())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(created.WorkerArtifactID()).To(Equal(workerArtifact.ID()))
		})
	})

	Describe("createdVolume.InitializeTaskCache", func() {
		Context("when there is a volume that belongs to worker task cache", func() {
			var (
				existingTaskCacheVolume db.CreatedVolume
				volume                  db.CreatedVolume
			)

			BeforeEach(func() {
				build, err := defaultTeam.CreateOneOffBuild()
				Expect(err).ToNot(HaveOccurred())

				creatingContainer, err := defaultWorker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", defaultTeam.ID()), db.ContainerMetadata{})
				Expect(err).ToNot(HaveOccurred())

				v, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), creatingContainer, "some-path")
				Expect(err).ToNot(HaveOccurred())

				existingTaskCacheVolume, err = v.Created()
				Expect(err).ToNot(HaveOccurred())

				err = existingTaskCacheVolume.InitializeTaskCache(defaultJob.ID(), "some-step", "some-cache-path")
				Expect(err).ToNot(HaveOccurred())

				v, err = volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), creatingContainer, "some-other-path")
				Expect(err).ToNot(HaveOccurred())

				volume, err = v.Created()
				Expect(err).ToNot(HaveOccurred())
			})

			It("sets current volume as worker task cache volume", func() {
				taskCache, err := taskCacheFactory.FindOrCreate(defaultJob.ID(), "some-step", "some-cache-path")
				Expect(err).ToNot(HaveOccurred())

				createdVolume, found, err := volumeRepository.FindTaskCacheVolume(defaultTeam.ID(), defaultWorker.Name(), taskCache)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(createdVolume).ToNot(BeNil())
				Expect(createdVolume.Handle()).To(Equal(existingTaskCacheVolume.Handle()))

				err = volume.InitializeTaskCache(defaultJob.ID(), "some-step", "some-cache-path")
				Expect(err).ToNot(HaveOccurred())

				createdVolume, found, err = volumeRepository.FindTaskCacheVolume(defaultTeam.ID(), defaultWorker.Name(), taskCache)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(createdVolume).ToNot(BeNil())
				Expect(createdVolume.Handle()).To(Equal(volume.Handle()))

				Expect(existingTaskCacheVolume.Handle()).ToNot(Equal(volume.Handle()))
			})
		})
	})

	Describe("Container volumes", func() {
		It("returns volume type, container handle, mount path", func() {
			creatingVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
			createdVolume, err := creatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))
			Expect(createdVolume.ContainerHandle()).To(Equal(defaultCreatingContainer.Handle()))
			Expect(createdVolume.Path()).To(Equal("/path/to/volume"))

			_, createdVolume, err = volumeRepository.FindContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))
			Expect(createdVolume.ContainerHandle()).To(Equal(defaultCreatingContainer.Handle()))
			Expect(createdVolume.Path()).To(Equal("/path/to/volume"))
		})
	})

	Describe("Volumes created from a parent", func() {
		It("returns parent handle", func() {
			creatingParentVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
			createdParentVolume, err := creatingParentVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			childCreatingVolume, err := createdParentVolume.CreateChildForContainer(defaultCreatingContainer, "/path/to/child/volume")
			Expect(err).ToNot(HaveOccurred())
			childVolume, err := childCreatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(childVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))
			Expect(childVolume.ContainerHandle()).To(Equal(defaultCreatingContainer.Handle()))
			Expect(childVolume.Path()).To(Equal("/path/to/child/volume"))
			Expect(childVolume.ParentHandle()).To(Equal(createdParentVolume.Handle()))

			_, childVolume, err = volumeRepository.FindContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/child/volume")
			Expect(err).ToNot(HaveOccurred())
			Expect(childVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))
			Expect(childVolume.ContainerHandle()).To(Equal(defaultCreatingContainer.Handle()))
			Expect(childVolume.Path()).To(Equal("/path/to/child/volume"))
			Expect(childVolume.ParentHandle()).To(Equal(createdParentVolume.Handle()))
		})

		It("prevents the parent from being destroyed", func() {
			creatingParentVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
			createdParentVolume, err := creatingParentVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			childCreatingVolume, err := createdParentVolume.CreateChildForContainer(defaultCreatingContainer, "/path/to/child/volume")
			Expect(err).ToNot(HaveOccurred())
			_, err = childCreatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			_, err = createdParentVolume.Destroying()
			Expect(err).To(Equal(db.ErrVolumeCannotBeDestroyedWithChildrenPresent))
		})
	})

	Describe("Resource cache volumes", func() {
		It("returns volume type, resource type, resource version", func() {
			scenario := dbtest.Setup(
				builder.WithPipeline(atc.Config{
					ResourceTypes: atc.ResourceTypes{
						{
							Name: "some-type",
							Type: "some-base-resource-type",
							Source: atc.Source{
								"some-type": "source",
							},
						},
					},
				}),
				builder.WithResourceTypeVersions(
					"some-type",
					atc.Version{"some": "version"},
					atc.Version{"some-custom-type": "version"},
				),
			)

			build, err := scenario.Team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			resourceCache, err := resourceCacheFactory.FindOrCreateResourceCache(
				db.ForBuild(build.ID()),
				"some-type",
				atc.Version{"some": "version"},
				atc.Source{"some": "source"},
				atc.Params{"some": "params"},
				atc.VersionedResourceTypes{
					{
						ResourceType: atc.ResourceType{
							Name:   "some-type",
							Type:   "some-base-resource-type",
							Source: atc.Source{"some-type": "((source-param))"},
						},
						Version: atc.Version{"some-custom-type": "version"},
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())

			creatingContainer, err := defaultWorker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", defaultTeam.ID()), db.ContainerMetadata{
				Type:     "get",
				StepName: "some-resource",
			})
			Expect(err).ToNot(HaveOccurred())

			creatingVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), creatingContainer, "some-path")
			Expect(err).ToNot(HaveOccurred())

			createdVolume, err := creatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))

			err = createdVolume.InitializeResourceCache(resourceCache)
			Expect(err).ToNot(HaveOccurred())

			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeResource)))

			volumeResourceType, err := createdVolume.ResourceType()
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeResourceType.ResourceType.WorkerBaseResourceType.Name).To(Equal("some-base-resource-type"))
			Expect(volumeResourceType.ResourceType.WorkerBaseResourceType.Version).To(Equal("some-brt-version"))
			Expect(volumeResourceType.ResourceType.Version).To(Equal(atc.Version{"some-custom-type": "version"}))
			Expect(volumeResourceType.Version).To(Equal(atc.Version{"some": "version"}))

			createdVolume, found, err := volumeRepository.FindResourceCacheVolume(defaultWorker.Name(), resourceCache)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeResource)))
			volumeResourceType, err = createdVolume.ResourceType()
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeResourceType.ResourceType.WorkerBaseResourceType.Name).To(Equal("some-base-resource-type"))
			Expect(volumeResourceType.ResourceType.WorkerBaseResourceType.Version).To(Equal("some-brt-version"))
			Expect(volumeResourceType.ResourceType.Version).To(Equal(atc.Version{"some-custom-type": "version"}))
			Expect(volumeResourceType.Version).To(Equal(atc.Version{"some": "version"}))
		})

		It("returns volume type from streamed source volume", func() {
			scenario := dbtest.Setup(
				builder.WithPipeline(atc.Config{
					ResourceTypes: atc.ResourceTypes{
						{
							Name: "some-type",
							Type: "some-base-resource-type",
							Source: atc.Source{
								"some-type": "source",
							},
						},
					},
				}),
				builder.WithResourceTypeVersions(
					"some-type",
					atc.Version{"some": "version"},
					atc.Version{"some-custom-type": "version"},
				),
				builder.WithWorker(atc.Worker{
					Name:     "weird-worker",
					Platform: "weird",

					GardenAddr:      "weird-garden-addr",
					BaggageclaimURL: "weird-baggageclaim-url",
				}),
			)

			sourceWorker := scenario.Workers[0]
			destinationWorker := scenario.Workers[1]

			build, err := scenario.Team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			resourceCache, err := resourceCacheFactory.FindOrCreateResourceCache(
				db.ForBuild(build.ID()),
				"some-type",
				atc.Version{"some": "version"},
				atc.Source{"some": "source"},
				atc.Params{"some": "params"},
				atc.VersionedResourceTypes{
					{
						ResourceType: atc.ResourceType{
							Name:   "some-type",
							Type:   dbtest.BaseResourceType,
							Source: atc.Source{"some-type": "((source-param))"},
						},
						Version: atc.Version{"some-custom-type": "version"},
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())

			creatingSourceContainer, err := sourceWorker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", defaultTeam.ID()), db.ContainerMetadata{
				Type:     "get",
				StepName: "some-resource",
			})
			Expect(err).ToNot(HaveOccurred())

			creatingSourceVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), sourceWorker.Name(), creatingSourceContainer, "some-path")
			Expect(err).ToNot(HaveOccurred())

			sourceVolume, err := creatingSourceVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(sourceVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))

			err = sourceVolume.InitializeResourceCache(resourceCache)
			Expect(err).ToNot(HaveOccurred())

			Expect(sourceVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeResource)))

			creatingDestinationContainer, err := destinationWorker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", defaultTeam.ID()), db.ContainerMetadata{
				Type:     "get",
				StepName: "some-resource",
			})
			Expect(err).ToNot(HaveOccurred())

			creatingDestinationVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), destinationWorker.Name(), creatingDestinationContainer, "some-path")
			Expect(err).ToNot(HaveOccurred())

			destinationVolume, err := creatingDestinationVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(destinationVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeContainer)))

			err = destinationVolume.InitializeStreamedResourceCache(resourceCache, sourceWorker.Name())
			Expect(err).ToNot(HaveOccurred())

			volumeResourceType, err := destinationVolume.ResourceType()
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeResourceType.ResourceType.WorkerBaseResourceType.Name).To(Equal(dbtest.BaseResourceType))
			Expect(volumeResourceType.ResourceType.WorkerBaseResourceType.Version).To(Equal(dbtest.BaseResourceTypeVersion))
			Expect(volumeResourceType.ResourceType.Version).To(Equal(atc.Version{"some-custom-type": "version"}))
			Expect(volumeResourceType.Version).To(Equal(atc.Version{"some": "version"}))
		})
	})

	Describe("Resource type volumes", func() {
		It("returns volume type, base resource type name, base resource type version", func() {
			usedWorkerBaseResourceType, found, err := workerBaseResourceTypeFactory.Find("some-base-resource-type", defaultWorker)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			creatingVolume, err := volumeRepository.CreateBaseResourceTypeVolume(usedWorkerBaseResourceType)
			Expect(err).ToNot(HaveOccurred())
			createdVolume, err := creatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeResourceType)))
			volumeBaseResourceType, err := createdVolume.BaseResourceType()
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeBaseResourceType.Name).To(Equal("some-base-resource-type"))
			Expect(volumeBaseResourceType.Version).To(Equal("some-brt-version"))

			_, createdVolume, err = volumeRepository.FindBaseResourceTypeVolume(usedWorkerBaseResourceType)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdVolume.Type()).To(Equal(db.VolumeType(db.VolumeTypeResourceType)))
			volumeBaseResourceType, err = createdVolume.BaseResourceType()
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeBaseResourceType.Name).To(Equal("some-base-resource-type"))
			Expect(volumeBaseResourceType.Version).To(Equal("some-brt-version"))
		})
	})

	Describe("Task cache volumes", func() {
		It("returns volume type and task identifier", func() {
			taskCache, err := taskCacheFactory.FindOrCreate(defaultJob.ID(), "some-task", "some-path")
			Expect(err).ToNot(HaveOccurred())

			uwtc, err := workerTaskCacheFactory.FindOrCreate(db.WorkerTaskCache{
				WorkerName: defaultWorker.Name(),
				TaskCache:  taskCache,
			})
			Expect(err).ToNot(HaveOccurred())

			creatingVolume, err := volumeRepository.CreateTaskCacheVolume(defaultTeam.ID(), uwtc)
			Expect(err).ToNot(HaveOccurred())

			createdVolume, err := creatingVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			Expect(createdVolume.Type()).To(Equal(db.VolumeTypeTaskCache))

			pipelineID, pipelineRef, jobName, stepName, err := createdVolume.TaskIdentifier()
			Expect(err).ToNot(HaveOccurred())

			Expect(pipelineID).To(Equal(defaultPipeline.ID()))
			Expect(pipelineRef).To(Equal(defaultPipelineRef))
			Expect(jobName).To(Equal(defaultJob.Name()))
			Expect(stepName).To(Equal("some-task"))
		})
	})

	Describe("createdVolume.CreateChildForContainer", func() {
		var parentVolume db.CreatedVolume
		var creatingContainer db.CreatingContainer

		BeforeEach(func() {
			build, err := defaultTeam.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			creatingContainer, err = defaultWorker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", defaultTeam.ID()), db.ContainerMetadata{
				Type:     "task",
				StepName: "some-task",
			})
			Expect(err).ToNot(HaveOccurred())

			usedResourceCache, err := resourceCacheFactory.FindOrCreateResourceCache(
				db.ForBuild(build.ID()),
				"some-type",
				atc.Version{"some": "version"},
				atc.Source{"some": "source"},
				atc.Params{"some": "params"},
				atc.VersionedResourceTypes{
					{
						ResourceType: atc.ResourceType{
							Name:   "some-type",
							Type:   "some-base-resource-type",
							Source: atc.Source{"some-type": "source"},
						},
						Version: atc.Version{"some-custom-type": "version"},
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())

			creatingContainer, err := defaultWorker.CreateContainer(db.NewBuildStepContainerOwner(build.ID(), "some-plan", defaultTeam.ID()), db.ContainerMetadata{
				Type:     "get",
				StepName: "some-resource",
			})
			Expect(err).ToNot(HaveOccurred())

			creatingParentVolume, err := volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), creatingContainer, "some-path")
			Expect(err).ToNot(HaveOccurred())

			parentVolume, err = creatingParentVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			err = parentVolume.InitializeResourceCache(usedResourceCache)
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates volume for parent volume", func() {
			creatingChildVolume, err := parentVolume.CreateChildForContainer(creatingContainer, "some-path-3")
			Expect(err).ToNot(HaveOccurred())

			_, err = parentVolume.Destroying()
			Expect(err).To(HaveOccurred())

			createdChildVolume, err := creatingChildVolume.Created()
			Expect(err).ToNot(HaveOccurred())

			destroyingChildVolume, err := createdChildVolume.Destroying()
			Expect(err).ToNot(HaveOccurred())
			destroyed, err := destroyingChildVolume.Destroy()
			Expect(err).ToNot(HaveOccurred())
			Expect(destroyed).To(Equal(true))

			destroyingParentVolume, err := parentVolume.Destroying()
			Expect(err).ToNot(HaveOccurred())
			destroyed, err = destroyingParentVolume.Destroy()
			Expect(err).ToNot(HaveOccurred())
			Expect(destroyed).To(Equal(true))
		})
	})

	Context("when worker is no longer in database", func() {
		BeforeEach(func() {
			var err error
			_, err = volumeRepository.CreateContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
		})

		It("the container goes away from the db", func() {
			err := defaultWorker.Delete()
			Expect(err).ToNot(HaveOccurred())

			creatingVolume, createdVolume, err := volumeRepository.FindContainerVolume(defaultTeam.ID(), defaultWorker.Name(), defaultCreatingContainer, "/path/to/volume")
			Expect(err).ToNot(HaveOccurred())
			Expect(creatingVolume).To(BeNil())
			Expect(createdVolume).To(BeNil())
		})
	})
})
