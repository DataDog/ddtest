package testoptimization

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	testoptimizationstate "github.com/DataDog/ddtest/civisibility"
	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/DataDog/ddtest/stableconfig"
)

const autoDetectServiceName = ""

type testOptimizationCloseAction func()

type searchCommitsResponse struct {
	LocalCommits  []string
	RemoteCommits []string
	IsOk          bool
}

type TestOptimizationClient struct {
	apiTransport              api.Transport
	newAPITransport           func(serviceName string) api.Transport
	cacheManager              *CacheManager
	repositoryChangesUploader func() (int64, error)
	enableSignalHandler       bool

	initializationOnce   sync.Once
	settingsOnce         sync.Once
	testOptimizationOnce sync.Once
	closeActionsMutex    sync.Mutex
	closeActions         []testOptimizationCloseAction
	settings             *api.SettingsResponseData
	knownTests           api.KnownTestsResponseData
	skippableTests       api.SkippableTests
	testManagementTests  api.TestManagementTestsResponseDataModules
}

func NewTestOptimizationClient() *TestOptimizationClient {
	return newTestOptimizationClient(nil, api.NewTransportWithServiceName, nil, true)
}

func NewTestOptimizationClientWithDependencies(apiTransport api.Transport) *TestOptimizationClient {
	return newTestOptimizationClient(apiTransport, nil, func() (int64, error) { return 0, nil }, false)
}

func newTestOptimizationClient(
	apiTransport api.Transport,
	newAPITransport func(serviceName string) api.Transport,
	repositoryChangesUploader func() (int64, error),
	enableSignalHandler bool,
) *TestOptimizationClient {
	if apiTransport == nil && newAPITransport == nil {
		newAPITransport = api.NewTransportWithServiceName
	}

	return &TestOptimizationClient{
		apiTransport:              apiTransport,
		newAPITransport:           newAPITransport,
		cacheManager:              NewCacheManager(),
		repositoryChangesUploader: repositoryChangesUploader,
		enableSignalHandler:       enableSignalHandler,
	}
}

func (c *TestOptimizationClient) Initialize(tags map[string]string) error {
	utils.AddCITagsMap(tags)

	startTime := time.Now()
	c.ensureTestOptimizationSessionInitialized()

	// Fetch and store settings.
	c.settings = c.GetSettings()

	duration := time.Since(startTime)
	slog.Debug("Finished Datadog Test Optimization initialization", "duration", duration)

	return nil
}

func (c *TestOptimizationClient) GetSettings() *api.SettingsResponseData {
	return c.ensureSettingsInitialization(autoDetectServiceName)
}

func (c *TestOptimizationClient) GetSkippableTests() map[string]bool {
	startTime := time.Now()

	slog.Debug("Fetching skippable tests...")
	c.ensureTestOptimizationInitialized()
	if c.skippableTests == nil {
		c.skippableTests = api.SkippableTests{}
	}

	if c.apiTransport != nil {
		if err := c.cacheManager.StoreSkippableTestsCache(c.apiTransport.GetSkippableTestsRawResponse()); err != nil {
			slog.Warn("Failed to store skippable tests cache", "error", err)
		}
	}

	duration := time.Since(startTime)
	slog.Debug("Finished fetching skippable tests", "count", len(c.skippableTests), "duration", duration)

	return c.skippableTests
}

func (c *TestOptimizationClient) GetKnownTests() *api.KnownTestsResponseData {
	if c.settings == nil || !c.settings.KnownTestsEnabled {
		return nil
	}
	c.ensureTestOptimizationInitialized()
	return &c.knownTests
}

func (c *TestOptimizationClient) GetTestManagementTestsData() *api.TestManagementTestsResponseDataModules {
	if c.settings == nil || !c.settings.TestManagement.Enabled {
		return nil
	}
	c.ensureTestOptimizationInitialized()
	return &c.testManagementTests
}

func (c *TestOptimizationClient) GetDisabledTests() map[string]bool {
	return disabledTestsFromTestManagementData(c.GetTestManagementTestsData())
}

func (c *TestOptimizationClient) GetTestSuiteDurations() *api.TestSuiteDurationsResponseData {
	testOptimizationTransport := c.ensureAPITransport(autoDetectServiceName)
	if testOptimizationTransport == nil {
		return &api.TestSuiteDurationsResponseData{
			TestSuites: map[string]map[string]api.TestSuiteDurationInfo{},
		}
	}
	return testOptimizationTransport.GetTestSuiteDurations()
}

func (c *TestOptimizationClient) StoreCacheAndExit() {
	repositorySettings := c.GetSettings()
	if repositorySettings != nil {
		slog.Debug("Repository settings", "itr_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)
	}

	if c.apiTransport != nil {
		if err := c.cacheManager.StoreRepositorySettings(c.apiTransport.GetSettingsRawResponse()); err != nil {
			slog.Warn("Failed to store repository settings cache", "error", err)
		}

		if err := c.cacheManager.StoreKnownTestsCache(c.apiTransport.GetKnownTestsRawResponse()); err != nil {
			slog.Warn("Failed to store known tests cache", "error", err)
		}

		if err := c.cacheManager.StoreTestManagementTestsCache(c.apiTransport.GetTestManagementTestsRawResponse()); err != nil {
			slog.Warn("Failed to store test management tests cache", "error", err)
		}
	}

	c.exitTestOptimization()
}

func (c *TestOptimizationClient) ensureTestOptimizationSessionInitialized() {
	c.initializationOnce.Do(func() {
		testoptimizationstate.SetState(testoptimizationstate.StateInitializing)
		defer testoptimizationstate.SetState(testoptimizationstate.StateInitialized)

		slog.SetLogLoggerLevel(slog.LevelInfo)
		if enabled, _ := stableconfig.Bool("DD_TRACE_DEBUG", false); enabled {
			slog.SetLogLoggerLevel(slog.LevelDebug)
		}

		slog.Debug("testoptimization: initializing")

		_ = os.Setenv(ciConstants.TestOptimizationEnabledEnvironmentVariable, "1")
		_ = os.Setenv("DD_TRACE_SAMPLE_RATE", "1")

		ciTags := utils.GetCITags()
		if _, ok := ciTags[git.GitRepositoryURL]; !ok {
			slog.Debug("testoptimization: git repository URL tag was not detected")
		}

		if c.enableSignalHandler {
			c.registerSignalHandler()
		}
	})
}

func (c *TestOptimizationClient) ensureSettingsInitialization(serviceName string) *api.SettingsResponseData {
	c.settingsOnce.Do(func() {
		slog.Debug("testoptimization: initializing settings")
		defer slog.Debug("testoptimization: settings initialization complete")

		testOptimizationTransport := c.ensureAPITransport(serviceName)
		if testOptimizationTransport == nil {
			slog.Error("testoptimization: error getting the test optimization API client")
			return
		}

		uploadChannel := c.uploadRepositoryChangesAsync()
		waitUpload := func(timeout time.Duration) bool {
			select {
			case <-uploadChannel:
				return true
			case <-time.After(timeout):
				slog.Warn("testoptimization: timeout waiting for upload repository changes")
				return false
			}
		}
		waitUploadFactory := func(timeout time.Duration) func() {
			return func() { waitUpload(timeout) }
		}

		ciSettings, err := testOptimizationTransport.GetSettings()
		if err != nil || ciSettings == nil {
			if err != nil {
				slog.Error("testoptimization: error getting test optimization settings", "error", err.Error())
			} else {
				slog.Error("testoptimization: error getting test optimization settings")
			}
			slog.Debug("testoptimization: no need to wait for the git upload to finish")
			c.pushTestOptimizationCloseAction(waitUploadFactory(time.Minute))
			return
		}

		if ciSettings.RequireGit {
			slog.Debug("testoptimization: waiting for the git upload to finish and repeating the settings request")
			if !waitUpload(time.Minute) {
				slog.Error("testoptimization: error getting test optimization settings due to timeout")
				return
			}
			ciSettings, err = testOptimizationTransport.GetSettings()
			if err != nil || ciSettings == nil {
				if err != nil {
					slog.Error("testoptimization: error getting test optimization settings", "error", err.Error())
				} else {
					slog.Error("testoptimization: error getting test optimization settings")
				}
				return
			}
		}

		applyEnvironmentOverrides(ciSettings)

		slog.Debug("testoptimization: no need to wait for the git upload to finish")
		c.pushTestOptimizationCloseAction(waitUploadFactory(time.Minute))
		c.settings = ciSettings
	})

	return c.settings
}

func (c *TestOptimizationClient) ensureAPITransport(serviceName string) api.Transport {
	if c.apiTransport != nil {
		return c.apiTransport
	}
	if c.newAPITransport == nil {
		return nil
	}
	c.apiTransport = c.newAPITransport(serviceName)
	return c.apiTransport
}

func applyEnvironmentOverrides(ciSettings *api.SettingsResponseData) {
	if !ciSettings.KnownTestsEnabled {
		ciSettings.EarlyFlakeDetection.Enabled = false
	}

	if ciSettings.FlakyTestRetriesEnabled && !testoptimizationstate.BoolEnv(ciConstants.TestOptimizationFlakyRetryEnabledEnvironmentVariable, true) {
		slog.Warn("testoptimization: flaky test retries was disabled by the environment variable")
		ciSettings.FlakyTestRetriesEnabled = false
	}

	if ciSettings.TestManagement.Enabled && !testoptimizationstate.BoolEnv(ciConstants.TestOptimizationManagementEnabledEnvironmentVariable, true) {
		slog.Warn("testoptimization: test management was disabled by the environment variable")
		ciSettings.TestManagement.Enabled = false
	}

	testManagementAttemptToFixRetriesEnv := testoptimizationstate.IntEnv(ciConstants.TestOptimizationAttemptToFixRetriesEnvironmentVariable, -1)
	if testManagementAttemptToFixRetriesEnv != -1 {
		ciSettings.TestManagement.AttemptToFixRetries = testManagementAttemptToFixRetriesEnv
	}
}

func (c *TestOptimizationClient) ensureTestOptimizationInitialized() {
	c.testOptimizationOnce.Do(func() {
		slog.Debug("testoptimization: initializing test optimization")
		defer slog.Debug("testoptimization: test optimization initialization complete")

		currentSettings := c.GetSettings()
		if currentSettings == nil || c.apiTransport == nil {
			return
		}

		additionalTags := map[string]string{
			ciConstants.LibraryCapabilitiesEarlyFlakeDetection:        "1",
			ciConstants.LibraryCapabilitiesAutoTestRetries:            "1",
			ciConstants.LibraryCapabilitiesTestImpactAnalysis:         "1",
			ciConstants.LibraryCapabilitiesTestManagementQuarantine:   "1",
			ciConstants.LibraryCapabilitiesTestManagementDisable:      "1",
			ciConstants.LibraryCapabilitiesTestManagementAttemptToFix: "5",
		}
		defer func() {
			if len(additionalTags) > 0 {
				slog.Debug("testoptimization: adding additional tags", "tags", additionalTags) //nolint:gocritic // Map structure logging for debugging
				utils.AddCITagsMap(additionalTags)
			}
		}()

		var additionalTagsMutex sync.Mutex
		setAdditionalTags := func(key string, value string) {
			additionalTagsMutex.Lock()
			defer additionalTagsMutex.Unlock()
			additionalTags[key] = value
		}

		var wg sync.WaitGroup

		if currentSettings.KnownTestsEnabled {
			wg.Add(1)
			go func() {
				defer wg.Done()
				knownTests, err := c.apiTransport.GetKnownTests()
				if err != nil {
					slog.Error("testoptimization: error getting test optimization known tests data", "err", err.Error())
				} else if knownTests != nil {
					c.knownTests = *knownTests
					slog.Debug("testoptimization: known tests data loaded.")
				}
			}()
		}

		if currentSettings.TestsSkipping {
			wg.Add(1)
			go func() {
				defer wg.Done()
				correlationID, skippableTests, err := c.apiTransport.GetSkippableTests()
				if err != nil {
					slog.Error("testoptimization: error getting test optimization skippable tests", "err", err.Error())
				} else if skippableTests != nil {
					slog.Debug("testoptimization: skippable tests loaded", "count", len(skippableTests))
					setAdditionalTags(ciConstants.ItrCorrelationIDTag, correlationID)
					c.skippableTests = skippableTests
				}
			}()
		}

		if currentSettings.TestManagement.Enabled {
			wg.Add(1)
			go func() {
				defer wg.Done()
				testManagementTests, err := c.apiTransport.GetTestManagementTests()
				if err != nil {
					slog.Error("testoptimization: error getting test optimization test management tests", "err", err.Error())
				} else if testManagementTests != nil {
					c.testManagementTests = *testManagementTests
					slog.Debug("testoptimization: test management loaded", "attemptToFixRetries", currentSettings.TestManagement.AttemptToFixRetries)
				}
			}()
		}

		wg.Wait()
	})
}

func (c *TestOptimizationClient) pushTestOptimizationCloseAction(action testOptimizationCloseAction) {
	c.closeActionsMutex.Lock()
	defer c.closeActionsMutex.Unlock()
	c.closeActions = append([]testOptimizationCloseAction{action}, c.closeActions...)
}

func (c *TestOptimizationClient) exitTestOptimization() {
	if testoptimizationstate.GetState() != testoptimizationstate.StateInitialized {
		slog.Debug("testoptimization: already closed or not initialized")
		return
	}

	testoptimizationstate.SetState(testoptimizationstate.StateExiting)
	defer testoptimizationstate.SetState(testoptimizationstate.StateExited)
	slog.Debug("testoptimization: exiting")

	c.closeActionsMutex.Lock()
	defer c.closeActionsMutex.Unlock()
	defer func() {
		c.closeActions = []testOptimizationCloseAction{}
		slog.Debug("testoptimization: done.")
	}()
	for _, action := range c.closeActions {
		action()
	}
}

func (c *TestOptimizationClient) registerSignalHandler() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		c.StoreCacheAndExit()
		os.Exit(1)
	}()
}

func (c *TestOptimizationClient) uploadRepositoryChangesAsync() chan struct{} {
	uploadChannel := make(chan struct{})
	go func() {
		defer close(uploadChannel)
		bytes, err := c.uploadRepositoryChanges()
		if err != nil {
			slog.Error("testoptimization: error uploading repository changes:", "error", err.Error())
		} else {
			slog.Debug("testoptimization: uploaded bytes in pack files", "count", bytes)
		}
	}()
	return uploadChannel
}

func (c *TestOptimizationClient) uploadRepositoryChanges() (bytes int64, err error) {
	if c.repositoryChangesUploader != nil {
		return c.repositoryChangesUploader()
	}

	return c.uploadRepositoryChangesFromGit()
}

func (c *TestOptimizationClient) uploadRepositoryChangesFromGit() (bytes int64, err error) {
	initialCommitData, err := c.getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("testoptimization: error getting the search commits response: %s", err)
	}

	if !initialCommitData.IsOk {
		return 0, nil
	}

	if !initialCommitData.hasCommits() {
		slog.Debug("testoptimization: no commits found")
		return 0, nil
	}

	if initialCommitData.hasCommits() && len(initialCommitData.missingCommits()) == 0 {
		slog.Debug("testoptimization: initial commit data has everything already, we don't need to upload anything")
		return 0, nil
	}

	hasBeenUnshallowed, err := git.UnshallowGitRepository()
	if err != nil || !hasBeenUnshallowed {
		if err != nil {
			slog.Warn(err.Error())
		}
		return c.sendObjectsPackFile(initialCommitData.LocalCommits[0], initialCommitData.missingCommits(), initialCommitData.RemoteCommits)
	}

	commitsData, err := c.getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("testoptimization: error getting the search commits response: %s", err)
	}

	if !commitsData.IsOk {
		return 0, nil
	}

	return c.sendObjectsPackFile(commitsData.LocalCommits[0], commitsData.missingCommits(), commitsData.RemoteCommits)
}

func (c *TestOptimizationClient) getSearchCommits() (*searchCommitsResponse, error) {
	localCommits := git.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		slog.Debug("testoptimization: no local commits found")
		return newSearchCommitsResponse(nil, nil, false), nil
	}

	if c.apiTransport == nil {
		return newSearchCommitsResponse(nil, nil, false), nil
	}

	slog.Debug("testoptimization: local commits found", "count", len(localCommits))
	remoteCommits, err := c.apiTransport.GetCommits(localCommits)
	return newSearchCommitsResponse(localCommits, remoteCommits, true), err
}

func newSearchCommitsResponse(localCommits []string, remoteCommits []string, isOk bool) *searchCommitsResponse {
	return &searchCommitsResponse{
		LocalCommits:  localCommits,
		RemoteCommits: remoteCommits,
		IsOk:          isOk,
	}
}

func (r *searchCommitsResponse) hasCommits() bool {
	return len(r.LocalCommits) > 0
}

func (r *searchCommitsResponse) missingCommits() []string {
	var missingCommits []string
	for _, localCommit := range r.LocalCommits {
		if !slices.Contains(r.RemoteCommits, localCommit) {
			missingCommits = append(missingCommits, localCommit)
		}
	}

	return missingCommits
}

func (c *TestOptimizationClient) sendObjectsPackFile(commitSha string, commitsToInclude []string, commitsToExclude []string) (bytes int64, err error) {
	packFiles := git.CreatePackFiles(commitsToInclude, commitsToExclude)
	if len(packFiles) == 0 {
		slog.Debug("testoptimization: no pack files to send")
		return 0, nil
	}

	slog.Debug("testoptimization: sending pack file with missing commits", "count", packFiles) //nolint:gocritic // File list logging for debugging

	defer cleanupPackFiles(packFiles)

	return c.apiTransport.SendPackFiles(commitSha, packFiles)
}

func cleanupPackFiles(packFiles []string) {
	packDirectories := make(map[string]struct{})
	for _, packFile := range packFiles {
		_ = os.Remove(packFile)
		packDirectories[filepath.Dir(packFile)] = struct{}{}
	}
	for packDirectory := range packDirectories {
		_ = os.RemoveAll(packDirectory)
	}
}

func disabledTestsFromTestManagementData(testManagementTests *api.TestManagementTestsResponseDataModules) map[string]bool {
	disabledTests := make(map[string]bool)
	if testManagementTests == nil {
		return disabledTests
	}

	for module, suites := range testManagementTests.Modules {
		for suite, tests := range suites.Suites {
			for name, test := range tests.Tests {
				if !test.Properties.Disabled || test.Properties.AttemptToFix {
					continue
				}
				disabledTest := Test{
					Module: module,
					Suite:  suite,
					Name:   name,
				}
				disabledTests[disabledTest.FQN()] = true
			}
		}
	}

	return disabledTests
}
