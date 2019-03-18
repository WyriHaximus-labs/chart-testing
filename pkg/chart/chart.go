// Copyright The Helm Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chart

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/helm/chart-testing/pkg/exec"

	"github.com/helm/chart-testing/pkg/config"
	"github.com/helm/chart-testing/pkg/tool"
	"github.com/helm/chart-testing/pkg/util"
	"github.com/pkg/errors"
)

// Git is the Interface that wraps Git operations.
//
// FileExistsOnBranch checks whether file exists on the specified remote/branch.
//
// Show returns the contents of file on the specified remote/branch.
//
// AddWorkingTree checks out the contents of the repository at a commit ref into the specified path.
//
// RemoveWorkingTree removes the working tree at the specified path.
//
// MergeBase returns the SHA1 of the merge base of commit1 and commit2.
//
// ListChangedFilesInDirs diffs commit against HEAD and returns changed files for the specified dirs.
//
// GetUrlForRemote returns the repo URL for the specified remote.
//
// ValidateRepository checks that the current working directory is a valid git repository,
// and returns nil if valid.
type Git interface {
	FileExistsOnBranch(file string, remote string, branch string) bool
	Show(file string, remote string, branch string) (string, error)
	AddWorkingTree(path string, ref string) error
	RemoveWorkingTree(path string) error
	MergeBase(commit1 string, commit2 string) (string, error)
	ListChangedFilesInDirs(commit string, dirs ...string) ([]string, error)
	GetUrlForRemote(remote string) (string, error)
	ValidateRepository() error
}

// Helm is the interface that wraps Helm operations
//
// Init runs client-side Helm initialization
//
// AddRepo adds a chart repository to the local Helm configuration
//
// BuildDependencies builds the chart's dependencies
//
// LintWithValues runs `helm lint` for the given chart using the specified values file.
// Pass a zero value for valuesFile in order to run lint without specifying a values file.
//
// InstallWithValues runs `helm install` for the given chart using the specified values file.
// Pass a zero value for valuesFile in order to run install without specifying a values file.
//
// Upgrade runs `helm upgrade` against an existing release, and re-uses the previously computed values.
//
// Test runs `helm test` against an existing release. Set the cleanup argument to true in order
// to clean up test pods created by helm after the test command completes.
//
// DeleteRelease purges the specified Helm release.
type Helm interface {
	Init() error
	AddRepo(name string, url string, extraArgs []string) error
	BuildDependencies(chart string) error
	LintWithValues(chart string, valuesFile string) error
	InstallWithValues(chart string, valuesFile string, namespace string, release string) error
	Upgrade(chart string, release string) error
	Test(release string, cleanup bool) error
	DeleteRelease(release string)
}

// Kubectl is the interface that wraps kubectl operations
//
// DeleteNamespace deletes a namespace
//
// WaitForDeployments waits for a deployment to become ready
//
// GetPodsforDeployment gets all pods for a deployment
//
// GetPods gets pods for the given args
//
// DescribePod prints the pod's description
//
// Logs prints the logs of container
//
// GetInitContainers gets all init containers of pod
//
// GetContainers gets all containers of pod
type Kubectl interface {
	DeleteNamespace(namespace string)
	WaitForDeployments(namespace string, selector string) error
	GetPodsforDeployment(namespace string, deployment string) ([]string, error)
	GetPods(args ...string) ([]string, error)
	DescribePod(namespace string, pod string) error
	Logs(namespace string, pod string, container string) error
	GetInitContainers(namespace string, pod string) ([]string, error)
	GetContainers(namespace string, pod string) ([]string, error)
}

// Linter is the interface that wrap linting operations
//
// YamlLint runs `yamllint` on the specified file with the specified configuration
//
// Yamale runs `yamale` on the specified file with the specified schema file
type Linter interface {
	YamlLint(yamlFile string, configFile string) error
	Yamale(yamlFile string, schemaFile string) error
}

// DirectoryLister is the interface
//
// ListChildDirs lists direct child directories of parentDir given they pass the test function
type DirectoryLister interface {
	ListChildDirs(parentDir string, test func(string) bool) ([]string, error)
}

// ChartUtils is the interface that wraps chart-related methods
//
// LookupChartDir looks up the chart's root directory based on some chart file that has changed
//
// ReadChartYaml reads the `Chart.yaml` from the specified directory
type ChartUtils interface {
	LookupChartDir(chartDirs []string, dir string) (string, error)
	ReadChartYaml(dir string) (*util.ChartYaml, error)
}

// AccountValidator is the interface that wraps Git account validation
//
// Validate checks if account is valid on repoDomain
type AccountValidator interface {
	Validate(repoDomain string, account string) error
}

type Testing struct {
	config           config.Configuration
	helm             Helm
	kubectl          Kubectl
	git              Git
	linter           Linter
	accountValidator AccountValidator
	directoryLister  DirectoryLister
	chartUtils       ChartUtils
}

// TestResults holds results and overall status
type TestResults struct {
	OverallSuccess bool
	TestResults    []TestResult
}

// TestResult holds test results for a specific chart
type TestResult struct {
	Chart string
	Error error
}

// NewTesting creates a new Testing struct with the given config.
func NewTesting(config config.Configuration) Testing {
	procExec := exec.NewProcessExecutor(config.Debug)
	extraArgs := strings.Fields(config.HelmExtraArgs)
	return Testing{
		config:           config,
		helm:             tool.NewHelm(procExec, extraArgs),
		git:              tool.NewGit(procExec),
		kubectl:          tool.NewKubectl(procExec),
		linter:           tool.NewLinter(procExec),
		accountValidator: tool.AccountValidator{},
		directoryLister:  util.DirectoryLister{},
		chartUtils:       util.ChartUtils{},
	}
}

const ctPreviousRevisionTree = "ct_previous_revision"

// computePreviousRevisionPath converts any file or directory path to the same path in the
// previous revision's working tree.
func computePreviousRevisionPath(dir string) string {
	return path.Join(ctPreviousRevisionTree, dir)
}

func (t *Testing) processCharts(action func(chart string, valuesFiles []string) TestResult) ([]TestResult, error) {
	var results []TestResult
	charts, err := t.FindChartsToBeProcessed()
	if err != nil {
		return nil, errors.Wrap(err, "Error identifying charts to process")
	} else if len(charts) == 0 {
		return results, nil
	}

	fmt.Println()
	util.PrintDelimiterLine("-")
	fmt.Println(" Charts to be processed:")
	util.PrintDelimiterLine("-")
	for _, chart := range charts {
		fmt.Printf(" %s\n", chart)
	}
	util.PrintDelimiterLine("-")
	fmt.Println()

	if err := t.helm.Init(); err != nil {
		return nil, errors.Wrap(err, "Error initializing Helm")
	}

	repoArgs := map[string][]string{}

	for _, repo := range t.config.HelmRepoExtraArgs {
		repoSlice := strings.SplitN(repo, "=", 2)
		name := repoSlice[0]
		repoExtraArgs := strings.Fields(repoSlice[1])
		repoArgs[name] = repoExtraArgs
	}

	for _, repo := range t.config.ChartRepos {
		repoSlice := strings.SplitN(repo, "=", 2)
		name := repoSlice[0]
		url := repoSlice[1]

		repoExtraArgs := repoArgs[name]
		if err := t.helm.AddRepo(name, url, repoExtraArgs); err != nil {
			return nil, errors.Wrapf(err, "Error adding repo: %s=%s", name, url)
		}
	}

	testResults := TestResults{
		OverallSuccess: true,
		TestResults:    results,
	}

	// Checkout previous chart revisions and build their dependencies
	if t.config.Upgrade {
		mergeBase, err := t.computeMergeBase()
		if err != nil {
			return results, errors.Wrap(err, "Error identifying merge base")
		}
		t.git.AddWorkingTree(ctPreviousRevisionTree, mergeBase)
		defer t.git.RemoveWorkingTree(ctPreviousRevisionTree)

		for _, chart := range charts {
			if err := t.helm.BuildDependencies(computePreviousRevisionPath(chart)); err != nil {
				// Only print error (don't exit) if building dependencies for previous revision fails.
				fmt.Println(errors.Wrapf(err, "Error building dependencies for previous revision of chart '%s'\n", chart))
			}
		}
	}

	for _, chart := range charts {
		valuesFiles := t.FindValuesFilesForCI(chart)

		if err := t.helm.BuildDependencies(chart); err != nil {
			return nil, errors.Wrapf(err, "Error building dependencies for chart '%s'", chart)
		}

		result := action(chart, valuesFiles)
		if result.Error != nil {
			testResults.OverallSuccess = false
		}
		results = append(results, result)
	}
	if testResults.OverallSuccess {
		return results, nil
	}

	return results, errors.New("Error processing charts")
}

// LintCharts lints charts (changed, all, specific) depending on the configuration.
func (t *Testing) LintCharts() ([]TestResult, error) {
	return t.processCharts(t.LintChart)
}

// InstallCharts install charts (changed, all, specific) depending on the configuration.
func (t *Testing) InstallCharts() ([]TestResult, error) {
	return t.processCharts(t.InstallChart)
}

// LintAndInstallCharts first lints and then installs charts (changed, all, specific) depending on the configuration.
func (t *Testing) LintAndInstallCharts() ([]TestResult, error) {
	return t.processCharts(t.LintAndInstallChart)
}

// PrintResults writes test results to stdout.
func (t *Testing) PrintResults(results []TestResult) {
	util.PrintDelimiterLine("-")
	if results != nil {
		for _, result := range results {
			err := result.Error
			if err != nil {
				fmt.Printf(" %s %s > %s\n", "✖︎", result.Chart, err)
			} else {
				fmt.Printf(" %s %s\n", "✔︎", result.Chart)
			}
		}
	} else {
		fmt.Println("No chart changes detected.")
	}
	util.PrintDelimiterLine("-")
}

// LintChart lints the specified chart.
func (t *Testing) LintChart(chart string, valuesFiles []string) TestResult {
	fmt.Printf("Linting chart '%s'\n", chart)

	result := TestResult{Chart: chart}

	if t.config.CheckVersionIncrement {
		if err := t.CheckVersionIncrement(chart); err != nil {
			result.Error = err
			return result
		}
	}

	chartYaml := path.Join(chart, "Chart.yaml")
	valuesYaml := path.Join(chart, "values.yaml")

	if t.config.ValidateChartSchema {
		if err := t.linter.Yamale(chartYaml, t.config.ChartYamlSchema); err != nil {
			result.Error = err
			return result
		}
	}

	if t.config.ValidateYaml {
		yamlFiles := append([]string{chartYaml, valuesYaml}, valuesFiles...)
		for _, yamlFile := range yamlFiles {
			if err := t.linter.YamlLint(yamlFile, t.config.LintConf); err != nil {
				result.Error = err
				return result
			}
		}
	}

	if t.config.ValidateMaintainers {
		if err := t.ValidateMaintainers(chart); err != nil {
			result.Error = err
			return result
		}
	}

	// Lint with defaults if no values files are specified.
	if len(valuesFiles) == 0 {
		valuesFiles = append(valuesFiles, "")
	}

	for _, valuesFile := range valuesFiles {
		if valuesFile != "" {
			fmt.Printf("\nLinting chart with values file '%s'...\n\n", valuesFile)
		}
		if err := t.helm.LintWithValues(chart, valuesFile); err != nil {
			result.Error = err
			break
		}
	}

	return result
}

// InstallChart installs the specified chart into a new namespace, waits for resources to become ready, and eventually
// uninstalls it and deletes the namespace again.
func (t *Testing) InstallChart(chart string, valuesFiles []string) TestResult {
	var result TestResult

	if t.config.Upgrade {
		// Test upgrade from previous version
		result = t.UpgradeChart(chart)
		if result.Error != nil {
			return result
		}
		// Test upgrade of current version (related: https://github.com/helm/chart-testing/issues/19)
		if err := t.doUpgrade(chart, chart, true); err != nil {
			result.Error = err
			return result
		}
	}

	result = TestResult{Chart: chart}
	if err := t.doInstall(chart); err != nil {
		result.Error = err
	}

	return result
}

// UpgradeChart tests in-place upgrades of the specified chart relative to its previous revisions. If the
// initial install or helm test of a previous revision of the chart fails, that release is ignored and no
// error will be returned. If the latest revision of the chart introduces a potentially breaking change
// according to the SemVer specification, upgrade testing will be skipped.
func (t *Testing) UpgradeChart(chart string) TestResult {
	result := TestResult{Chart: chart}

	breakingChangeAllowed, err := t.checkBreakingChangeAllowed(chart)

	if breakingChangeAllowed {
		if err != nil {
			fmt.Println(errors.Wrap(err, fmt.Sprintf("Skipping upgrade test of '%s' because", chart)))
		}
		return result
	} else if err != nil {
		fmt.Printf("Error comparing chart versions for '%s'\n", chart)
		result.Error = err
		return result
	}

	result.Error = t.doUpgrade(computePreviousRevisionPath(chart), chart, false)
	return result
}

func (t *Testing) doInstall(chart string) error {
	fmt.Printf("Installing chart '%s'...\n", chart)
	valuesFiles := t.FindValuesFilesForCI(chart)

	// Test with defaults if no values files are specified.
	if len(valuesFiles) == 0 {
		valuesFiles = append(valuesFiles, "")
	}

	for _, valuesFile := range valuesFiles {
		if valuesFile != "" {
			fmt.Printf("\nInstalling chart with values file '%s'...\n\n", valuesFile)
		}

		// Use anonymous function. Otherwise deferred calls would pile up
		// and be executed in reverse order after the loop.
		fun := func() error {
			namespace, release, releaseSelector, cleanup := t.generateInstallConfig(chart)
			defer cleanup()

			if err := t.helm.InstallWithValues(chart, valuesFile, namespace, release); err != nil {
				return err
			}
			return t.testRelease(release, namespace, releaseSelector, false)
		}

		if err := fun(); err != nil {
			return err
		}
	}

	return nil
}

func (t *Testing) doUpgrade(oldChart, newChart string, oldChartMustPass bool) error {
	fmt.Printf("Testing upgrades of chart '%s' relative to previous revision '%s'...\n", newChart, oldChart)
	valuesFiles := t.FindValuesFilesForCI(oldChart)
	if len(valuesFiles) == 0 {
		valuesFiles = append(valuesFiles, "")
	}
	for _, valuesFile := range valuesFiles {
		if valuesFile != "" {
			fmt.Printf("\nInstalling chart '%s' with values file '%s'...\n\n", oldChart, valuesFile)
		}

		// Use anonymous function. Otherwise deferred calls would pile up
		// and be executed in reverse order after the loop.
		fun := func() error {
			namespace, release, releaseSelector, cleanup := t.generateInstallConfig(oldChart)
			defer cleanup()

			// Install previous version of chart. If installation fails, ignore this release.
			if err := t.helm.InstallWithValues(oldChart, valuesFile, namespace, release); err != nil {
				if oldChartMustPass {
					return err
				}
				fmt.Println(errors.Wrap(err, fmt.Sprintf("Upgrade testing for release '%s' skipped because of previous revision installation error", release)))
				return nil
			}
			if err := t.testRelease(release, namespace, releaseSelector, true); err != nil {
				if oldChartMustPass {
					return err
				}
				fmt.Println(errors.Wrap(err, fmt.Sprintf("Upgrade testing for release '%s' skipped because of previous revision testing error", release)))
				return nil
			}

			if err := t.helm.Upgrade(oldChart, release); err != nil {
				return err
			}

			return t.testRelease(release, namespace, releaseSelector, false)
		}

		if err := fun(); err != nil {
			return err
		}
	}

	return nil
}

func (t *Testing) testRelease(release, namespace, releaseSelector string, cleanupHelmTests bool) error {
	if err := t.kubectl.WaitForDeployments(namespace, releaseSelector); err != nil {
		return err
	}
	if err := t.helm.Test(release, cleanupHelmTests); err != nil {
		return err
	}
	return nil
}

func (t *Testing) generateInstallConfig(chart string) (namespace, release, releaseSelector string, cleanup func()) {
	if t.config.Namespace != "" {
		namespace = t.config.Namespace
		release, _ = util.CreateInstallParams(chart, t.config.BuildId)
		releaseSelector = fmt.Sprintf("%s=%s", t.config.ReleaseLabel, release)
		cleanup = func() {
			t.PrintPodDetailsAndLogs(namespace, releaseSelector)
			t.helm.DeleteRelease(release)
		}
	} else {
		release, namespace = util.CreateInstallParams(chart, t.config.BuildId)
		cleanup = func() {
			t.PrintPodDetailsAndLogs(namespace, releaseSelector)
			t.helm.DeleteRelease(release)
			t.kubectl.DeleteNamespace(namespace)
		}
	}

	return
}

// LintAndInstallChart first lints and then installs the specified chart.
func (t *Testing) LintAndInstallChart(chart string, valuesFiles []string) TestResult {
	result := t.LintChart(chart, valuesFiles)
	if result.Error != nil {
		return result
	}
	return t.InstallChart(chart, valuesFiles)
}

// FindChartsToBeProcessed identifies charts to be processed depending on the configuration
// (changed charts, all charts, or specific charts).
func (t *Testing) FindChartsToBeProcessed() ([]string, error) {
	cfg := t.config
	if cfg.ProcessAllCharts {
		return t.ReadAllChartDirectories()
	} else if len(cfg.Charts) > 0 {
		return t.config.Charts, nil
	}
	return t.ComputeChangedChartDirectories()
}

// FindValuesFilesForCI returns all files in the 'ci' subfolder of the chart directory matching the pattern '*-values.yaml'
func (t *Testing) FindValuesFilesForCI(chart string) []string {
	ciDir := path.Join(chart, "ci/*-values.yaml")
	matches, _ := filepath.Glob(ciDir)
	return matches
}

func (t *Testing) computeMergeBase() (string, error) {
	err := t.git.ValidateRepository()
	if err != nil {
		return "", fmt.Errorf("Must be in a git repository")
	}
	return t.git.MergeBase(fmt.Sprintf("%s/%s", t.config.Remote, t.config.TargetBranch), "HEAD")
}

// ComputeChangedChartDirectories takes the merge base of HEAD and the configured remote and target branch and computes a
// slice of changed charts from that in the configured chart directories excluding those configured to be excluded.
func (t *Testing) ComputeChangedChartDirectories() ([]string, error) {
	cfg := t.config

	mergeBase, err := t.computeMergeBase()
	if err != nil {
		return nil, err
	}

	allChangedChartFiles, err := t.git.ListChangedFilesInDirs(mergeBase, cfg.ChartDirs...)
	if err != nil {
		return nil, errors.Wrap(err, "Error creating diff")
	}

	var changedChartDirs []string
	for _, file := range allChangedChartFiles {
		pathElements := strings.SplitN(filepath.ToSlash(file), "/", 3)
		if len(pathElements) < 2 || util.StringSliceContains(cfg.ExcludedCharts, pathElements[1]) {
			continue
		}
		dir := filepath.Dir(file)
		// Make sure directory is really a chart directory
		chartDir, err := t.chartUtils.LookupChartDir(cfg.ChartDirs, dir)
		if err == nil {
			// Only add it if not already in the list
			if !util.StringSliceContains(changedChartDirs, chartDir) {
				changedChartDirs = append(changedChartDirs, chartDir)
			}
		} else {
			fmt.Printf("Directory '%s' is no chart directory. Skipping...", chartDir)
		}
	}

	return changedChartDirs, nil
}

// ReadAllChartDirectories returns a slice of all charts in the configured chart directories except those
// configured to be excluded.
func (t *Testing) ReadAllChartDirectories() ([]string, error) {
	cfg := t.config

	var chartDirs []string
	for _, chartParentDir := range cfg.ChartDirs {
		dirs, err := t.directoryLister.ListChildDirs(chartParentDir,
			func(dir string) bool {
				_, err := t.chartUtils.LookupChartDir(cfg.ChartDirs, dir)
				return err == nil && !util.StringSliceContains(cfg.ExcludedCharts, path.Base(dir))
			})
		if err != nil {
			return nil, errors.Wrap(err, "Error reading chart directories")
		}
		chartDirs = append(chartDirs, dirs...)
	}

	return chartDirs, nil
}

// CheckVersionIncrement checks that the new chart version is greater than the old one using semantic version comparison.
func (t *Testing) CheckVersionIncrement(chart string) error {
	fmt.Printf("Checking chart '%s' for a version bump...\n", chart)

	oldVersion, err := t.GetOldChartVersion(chart)
	if err != nil {
		return err
	}
	if oldVersion == "" {
		// new chart, skip version check
		return nil
	}

	fmt.Println("Old chart version:", oldVersion)

	newVersion, err := t.GetNewChartVersion(chart)
	if err != nil {
		return err
	}
	fmt.Println("New chart version:", newVersion)

	result, err := util.CompareVersions(oldVersion, newVersion)
	if err != nil {
		return err
	}

	if result >= 0 {
		return errors.New("Chart version not ok. Needs a version bump!")
	}

	fmt.Println("Chart version ok.")
	return nil
}

func (t *Testing) checkBreakingChangeAllowed(chart string) (allowed bool, err error) {
	oldVersion, err := t.GetOldChartVersion(chart)
	if err != nil {
		return false, err
	}
	if oldVersion == "" {
		// new chart, skip upgrade check
		return true, fmt.Errorf("chart has no previous revision")
	}

	newVersion, err := t.GetNewChartVersion(chart)
	if err != nil {
		return false, err
	}

	return util.BreakingChangeAllowed(oldVersion, newVersion)
}

// GetOldChartVersion gets the version of the old Chart.yaml file from the target branch.
func (t *Testing) GetOldChartVersion(chart string) (string, error) {
	cfg := t.config

	chartYamlFile := path.Join(chart, "Chart.yaml")
	if !t.git.FileExistsOnBranch(chartYamlFile, cfg.Remote, cfg.TargetBranch) {
		fmt.Printf("Unable to find chart on %s. New chart detected.\n", cfg.TargetBranch)
		return "", nil
	}

	chartYamlContents, err := t.git.Show(chartYamlFile, cfg.Remote, cfg.TargetBranch)
	if err != nil {
		return "", errors.Wrap(err, "Error reading old Chart.yaml")
	}

	chartYaml, err := util.ReadChartYaml([]byte(chartYamlContents))
	if err != nil {
		return "", errors.Wrap(err, "Error reading old chart version")
	}

	return chartYaml.Version, nil
}

// GetNewChartVersion gets the new version from the currently checked out Chart.yaml file.
func (t *Testing) GetNewChartVersion(chart string) (string, error) {
	chartYaml, err := t.chartUtils.ReadChartYaml(chart)
	if err != nil {
		return "", errors.Wrap(err, "Error reading new chart version")
	}
	return chartYaml.Version, nil
}

// ValidateMaintainers validates maintainers in the Chart.yaml file. Maintainer names must be valid accounts
// (GitHub, Bitbucket, GitLab) names. Deprecated charts must not have maintainers.
func (t *Testing) ValidateMaintainers(chart string) error {
	fmt.Println("Validating maintainers...")

	chartYaml, err := t.chartUtils.ReadChartYaml(chart)
	if err != nil {
		return err
	}

	if chartYaml.Deprecated {
		if len(chartYaml.Maintainers) > 0 {
			return errors.New("Deprecated chart must not have maintainers")
		}
		return nil
	}

	if len(chartYaml.Maintainers) == 0 {
		return errors.New("Chart doesn't have maintainers")
	}

	repoUrl, err := t.git.GetUrlForRemote(t.config.Remote)
	if err != nil {
		return err
	}

	for _, maintainer := range chartYaml.Maintainers {
		if err := t.accountValidator.Validate(repoUrl, maintainer.Name); err != nil {
			return err
		}
	}

	return nil
}

func (t *Testing) PrintPodDetailsAndLogs(namespace string, selector string) {
	pods, err := t.kubectl.GetPods(
		"--no-headers",
		"--namespace",
		namespace,
		"--selector",
		selector,
		"--output",
		"jsonpath={.items[*].metadata.name}",
	)
	if err != nil {
		fmt.Println("Error printing logs:", err)
		return
	}

	util.PrintDelimiterLine("=")

	for _, pod := range pods {
		printDetails(pod, "Description of pod", "~", func(item string) error {
			return t.kubectl.DescribePod(namespace, pod)
		}, pod)

		initContainers, err := t.kubectl.GetInitContainers(namespace, pod)
		if err != nil {
			fmt.Println("Error printing logs:", err)
			return
		}

		printDetails(pod, "Logs of init container", "-",
			func(item string) error {
				return t.kubectl.Logs(namespace, pod, item)
			}, initContainers...)

		containers, err := t.kubectl.GetContainers(namespace, pod)
		if err != nil {
			fmt.Println("Error printing logs:", err)
			return
		}

		printDetails(pod, "Logs of container", "-",
			func(item string) error {
				return t.kubectl.Logs(namespace, pod, item)
			},
			containers...)
	}

	util.PrintDelimiterLine("=")
}

func printDetails(pod string, text string, delimiterChar string, printFunc func(item string) error, items ...string) {
	for _, item := range items {
		item = strings.Trim(item, "'")

		util.PrintDelimiterLine(delimiterChar)
		fmt.Printf("==> %s %s\n", text, pod)
		util.PrintDelimiterLine(delimiterChar)

		if err := printFunc(item); err != nil {
			fmt.Println("Error printing details:", err)
			return
		}

		util.PrintDelimiterLine(delimiterChar)
		fmt.Printf("<== %s %s\n", text, pod)
		util.PrintDelimiterLine(delimiterChar)
	}
}
