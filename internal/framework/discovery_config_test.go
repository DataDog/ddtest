package framework

import (
	"github.com/DataDog/ddtest/internal/ext"
)

func newTestRSpecWithExecutor(executor ext.CommandExecutor) *RSpec {
	rspec := NewRSpec()
	rspec.executor = executor
	rspec.discoveryConfig.Executor = executor
	return rspec
}

func newTestRSpecWithOverride(commandOverride []string) *RSpec {
	rspec := NewRSpec()
	rspec.commandOverride = commandOverride
	return rspec
}

func newTestRSpecWithExecutorAndOverride(executor ext.CommandExecutor, commandOverride []string) *RSpec {
	rspec := newTestRSpecWithExecutor(executor)
	rspec.commandOverride = commandOverride
	return rspec
}

func newTestMinitestWithExecutor(executor ext.CommandExecutor) *Minitest {
	minitest := NewMinitest()
	minitest.executor = executor
	minitest.discoveryConfig.Executor = executor
	return minitest
}

func newTestMinitestWithExecutorAndOverride(executor ext.CommandExecutor, commandOverride []string) *Minitest {
	minitest := newTestMinitestWithExecutor(executor)
	minitest.commandOverride = commandOverride
	return minitest
}
