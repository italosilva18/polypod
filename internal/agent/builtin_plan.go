package agent

import "reflect"

// builtin_plan.go provides a first-class *Agent for the plan persona,
// translated from the TypeScript planAgent.ts persona. The agent writes
// step-by-step implementation plans and reasons about architectural
// trade-offs without touching any files.

// planPersona is the system prompt for a read-only architect/planner agent.
// The prose is English because it is fed to the model directly.
const planPersona = `You are a software architect and planning specialist for Polypod. Your role is to explore the codebase and design implementation plans — step-by-step plans, architectural trade-offs, and clear sequencing — without writing any code.

You operate in strict read-only mode. You MUST NOT create, modify, delete, move, or copy files (including temporary files), use redirect operators or heredocs, or run any command that changes system state. You do not have access to file editing tools. If you try to edit a file, it will fail.

Your process:
1. Understand the requirements: read any files the caller points to and any relevant plan or spec.
2. Explore thoroughly: use read_file, grep, and list_directory to find existing patterns, conventions, and the current architecture. Identify similar features as references and trace relevant code paths.
3. Design the solution: propose an implementation approach consistent with existing patterns. Weigh trade-offs explicitly (simplicity vs flexibility, coupling, test surface, migration cost).
4. Detail the plan: give a step-by-step implementation strategy, identify dependencies and sequencing, and anticipate likely challenges.

End every response with a "Critical Files for Implementation" section listing 3-5 file paths most central to executing the plan.`

// PlanPersonaAgent returns a first-class *Agent for step-by-step
// implementation planning. It can read and grep but cannot edit or execute
// code. Skills are strictly read-only.
func PlanPersonaAgent() *Agent {
	return &Agent{
		Name:        "plan-persona",
		Description: "Software architect agent: step-by-step plans, architectural trade-offs, read-only analysis.",
		Persona:     planPersona,
		Skills:      []string{"read_file", "list_directory", "search_files", "grep"},
		Aliases:     []string{"planner-persona", "architect-persona"},
		IsBuiltIn:   true,
	}
}

// RegisterBuiltinAgents registers the three persona-based built-in agents
// (verification, explore, plan) into a registry. It accepts the registry
// as an `any` so it can work with either the concrete *Registry type used
// in this package or any other registry shape that exposes a
// `Register(a *Agent)` method.
//
// If the caller passes a *Registry, it is used directly. Otherwise, this
// function falls back to reflection and looks for a `Register` method with
// the signature `func(*Agent)`. Types that do not match either shape are
// silently ignored so callers cannot accidentally wire a broken registry.
func RegisterBuiltinAgents(registry any) {
	agents := []*Agent{
		VerificationPersonaAgent(),
		ExplorePersonaAgent(),
		PlanPersonaAgent(),
	}

	// Fast path: concrete *Registry.
	if r, ok := registry.(*Registry); ok && r != nil {
		for _, a := range agents {
			r.Register(a)
		}
		return
	}

	// Fallback: reflection-based dispatch so callers with a custom
	// registry shape still get sensible behavior.
	registerWithReflection(registry, agents)
}

// registerWithReflection attempts to call a Register(*Agent) method on an
// arbitrary registry value. If the method is absent or has the wrong
// signature, the call is silently ignored — callers already received the
// concrete-type fast path in RegisterBuiltinAgents.
func registerWithReflection(registry any, agents []*Agent) {
	if registry == nil {
		return
	}
	v := reflect.ValueOf(registry)
	if !v.IsValid() {
		return
	}
	m := v.MethodByName("Register")
	if !m.IsValid() {
		return
	}
	mt := m.Type()
	if mt.NumIn() != 1 {
		return
	}
	// Ensure the single parameter is assignable from *Agent.
	want := reflect.TypeOf((*Agent)(nil))
	if !want.AssignableTo(mt.In(0)) {
		return
	}
	for _, a := range agents {
		// Protect against panics from arbitrary third-party registries.
		func(a *Agent) {
			defer func() { _ = recover() }()
			m.Call([]reflect.Value{reflect.ValueOf(a)})
		}(a)
	}
}
