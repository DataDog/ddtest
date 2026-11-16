// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package integrations

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
)

func TestAttachedMirrorTrimpathParsesSourceAndClassifiesModifiedTest(t *testing.T) {
	if !currentBinaryWasBuiltWithTrimpath(t) {
		t.Skip("requires -trimpath")
	}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	configureSourcePathTestState(t, sourcePathRepositoryRoot(t))
	utils.AddCITagsMap(map[string]string{constants.GitRepositoryURL: "https://mirror.example.com/transport/repo.git", constants.GitBranch: "feature"})
	fn := runtime.FuncForPC(reflect.ValueOf(sourcePathFixtureFunc).Pointer())
	metadata := loadSourceFunctionMetadata(fn)
	const relative = "ddto/internal/thirdparty/dd-trace-go/civisibility/integrations/manual_api_sourcepath_test.go"
	if !metadata.fileMetadata.parseOK || metadata.sourcePath.RelativePath != relative || metadata.sourcePath.FilesystemPath != filepath.Join(directory, "manual_api_sourcepath_test.go") {
		t.Fatalf("mirror source metadata=%+v parsed=%t error=%v", metadata.sourcePath, metadata.fileMetadata.parseOK, metadata.fileMetadata.parseErr)
	}
	classifier := &recordingImpactedTestClassifier{result: true}
	if !isTestFuncModified(classifier, "TestSourceFixture", fn) || !classifier.called || classifier.source != relative || classifier.startLine <= 0 || classifier.endLine < classifier.startLine {
		t.Fatalf("modified-test classification lost parsed source: %+v", classifier)
	}
}
