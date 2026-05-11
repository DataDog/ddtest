package runner

import (
	"fmt"
	"os"
	"strings"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	ciUtils "github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/internal/constants"
)

func createWorkerEnv(workerEnvMap map[string]string, nodeIndex int, workerIndex int) map[string]string {
	workerEnv := replaceIndexPlaceholders(workerEnvMap, nodeIndex, workerIndex)
	ensureTestSessionName(workerEnv, nodeIndex, workerIndex)
	return workerEnv
}

func replaceIndexPlaceholders(workerEnvMap map[string]string, nodeIndex int, workerIndex int) map[string]string {
	workerEnv := make(map[string]string)
	for key, value := range workerEnvMap {
		workerEnv[key] = replaceIndexPlaceholdersInString(value, nodeIndex, workerIndex)
	}
	return workerEnv
}

func replaceIndexPlaceholdersInString(value string, nodeIndex int, workerIndex int) string {
	nodeIndexValue := fmt.Sprintf("%d", nodeIndex)
	workerIndexValue := fmt.Sprintf("%d", workerIndex)
	value = strings.ReplaceAll(value, constants.NodeIndexPlaceholder, nodeIndexValue)
	return strings.ReplaceAll(value, constants.WorkerIndexPlaceholder, workerIndexValue)
}

func ensureTestSessionName(workerEnv map[string]string, nodeIndex int, workerIndex int) {
	if _, ok := workerEnv[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable]; ok {
		return
	}

	if sessionName, ok := os.LookupEnv(ciConstants.CIVisibilityTestSessionNameEnvironmentVariable); ok {
		workerEnv[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable] = replaceIndexPlaceholdersInString(sessionName, nodeIndex, workerIndex)
		return
	}

	service := resolveServiceName(ciUtils.GetCITags()[ciConstants.GitRepositoryURL])
	workerEnv[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable] = fmt.Sprintf("%s-node-%d-worker-%d", service, nodeIndex, workerIndex)
}
