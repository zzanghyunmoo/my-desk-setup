package projectaction

import "slices"

type ActionKind string

const (
	ActionBuild     ActionKind = "build"
	ActionTest      ActionKind = "test"
	ActionRun       ActionKind = "run"
	ActionWatch     ActionKind = "watch"
	ActionDebugApp  ActionKind = "debug-app"
	ActionDebugTest ActionKind = "debug-test"
)

var commonActionOrder = []ActionKind{
	ActionBuild,
	ActionTest,
	ActionRun,
	ActionWatch,
	ActionDebugApp,
	ActionDebugTest,
}

func Order() []ActionKind {
	return slices.Clone(commonActionOrder)
}
