package main

import (
	"github.com/simtxng/cook/pkg/orchestrator"
	"github.com/simtxng/cook/pkg/packager/darwin"
	"github.com/simtxng/cook/pkg/packager/linux"
	"github.com/simtxng/cook/pkg/packager/windows"
)

func registerPlatformPackagers(o *orchestrator.Orchestrator) {
	o.Register(linux.NewPackager())
	o.Register(windows.New())
	o.Register(darwin.New())
}
