package constants

// Prompt constants migrated from Claude Code TypeScript source.
// Source: constants/prompts.ts, constants/cyberRiskInstruction.ts, constants/spinnerVerbs.ts,
//         constants/turnCompletionVerbs.ts

// ---------------------------------------------------------------------------
// System prompt sections
// ---------------------------------------------------------------------------

const (
	// SystemPromptDynamicBoundary separates static (cacheable) content from
	// dynamic content in the system prompt array.
	SystemPromptDynamicBoundary = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__"
)

// CyberRiskInstruction provides guidance for handling security-related requests.
// DO NOT MODIFY WITHOUT SAFEGUARDS TEAM REVIEW.
const CyberRiskInstruction = `IMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.`

// DefaultAgentPrompt is the default system prompt for subagents.
const DefaultAgentPrompt = `You are an agent for Claude Code, Anthropic's official CLI for Claude. Given the user's message, you should use the tools available to complete the task. Complete the task fully—don't gold-plate, but don't leave it half-done. When you complete the task, respond with a concise report covering what was done and any key findings — the caller will relay this to the user, so it only needs the essentials.`

// NoToolsPreamble is prepended when the model has no tools available.
const NoToolsPreamble = `You are an AI assistant. You do not have access to any tools. Answer the user's question based on your knowledge.`

// NoToolsTrailer is appended when the model has no tools available.
const NoToolsTrailer = `Remember: you cannot execute code, read files, or interact with the system. Provide your best answer based on your training data.`

// SummarizeToolResultsSection advises the model to retain important tool result info.
const SummarizeToolResultsSection = `When working with tool results, write down any important information you might need later in your response, as the original tool result may be cleared later.`

// ---------------------------------------------------------------------------
// Spinner verbs (loading messages)
// ---------------------------------------------------------------------------

// SpinnerVerbs are verbs shown while the model is thinking.
var SpinnerVerbs = []string{
	"Accomplishing", "Actioning", "Actualizing", "Architecting",
	"Baking", "Beaming", "Beboppin'", "Befuddling", "Billowing",
	"Blanching", "Bloviating", "Boogieing", "Boondoggling", "Booping",
	"Bootstrapping", "Brewing", "Bunning", "Burrowing",
	"Calculating", "Canoodling", "Caramelizing", "Cascading",
	"Catapulting", "Cerebrating", "Channeling", "Channelling",
	"Choreographing", "Churning", "Clauding", "Coalescing",
	"Cogitating", "Combobulating", "Composing", "Computing",
	"Concocting", "Considering", "Contemplating", "Cooking",
	"Crafting", "Creating", "Crunching", "Crystallizing", "Cultivating",
	"Deciphering", "Deliberating", "Determining", "Dilly-dallying",
	"Discombobulating", "Doing", "Doodling", "Drizzling",
	"Ebbing", "Effecting", "Elucidating", "Embellishing",
	"Enchanting", "Envisioning", "Evaporating",
	"Fermenting", "Fiddle-faddling", "Finagling", "Flambéing",
	"Flibbertigibbeting", "Flowing", "Flummoxing", "Fluttering",
	"Forging", "Forming", "Frolicking", "Frosting",
	"Gallivanting", "Galloping", "Garnishing", "Generating",
	"Gesticulating", "Germinating", "Gitifying", "Grooving", "Gusting",
	"Harmonizing", "Hashing", "Hatching", "Herding", "Honking",
	"Hullaballooing", "Hyperspacing",
	"Ideating", "Imagining", "Improvising", "Incubating",
	"Inferring", "Infusing", "Ionizing",
	"Jitterbugging", "Julienning",
	"Kneading",
	"Leavening", "Levitating", "Lollygagging",
	"Manifesting", "Marinating", "Meandering", "Metamorphosing",
	"Misting", "Moonwalking", "Moseying", "Mulling", "Mustering", "Musing",
	"Nebulizing", "Nesting", "Newspapering", "Noodling", "Nucleating",
	"Orbiting", "Orchestrating", "Osmosing",
	"Perambulating", "Percolating", "Perusing", "Philosophising",
	"Photosynthesizing", "Pollinating", "Pondering", "Pontificating",
	"Pouncing", "Precipitating", "Prestidigitating", "Processing",
	"Proofing", "Propagating", "Puttering", "Puzzling",
	"Quantumizing",
	"Razzle-dazzling", "Razzmatazzing", "Recombobulating",
	"Reticulating", "Roosting", "Ruminating",
	"Sautéing", "Scampering", "Schlepping", "Scurrying", "Seasoning",
	"Shenaniganing", "Shimmying", "Simmering", "Skedaddling",
	"Sketching", "Slithering", "Smooshing", "Sock-hopping",
	"Spelunking", "Spinning", "Sprouting", "Stewing", "Sublimating",
	"Swirling", "Swooping", "Symbioting", "Synthesizing",
	"Tempering", "Thinking", "Thundering", "Tinkering",
	"Tomfoolering", "Topsy-turvying", "Transfiguring", "Transmuting", "Twisting",
	"Undulating", "Unfurling", "Unravelling",
	"Vibing",
	"Waddling", "Wandering", "Warping", "Whatchamacalliting",
	"Whirlpooling", "Whirring", "Whisking", "Wibbling", "Working", "Wrangling",
	"Zesting", "Zigzagging",
}

// ---------------------------------------------------------------------------
// Turn completion verbs
// ---------------------------------------------------------------------------

// TurnCompletionVerbs are past tense verbs for turn completion messages.
// They work naturally with "for [duration]" (e.g., "Worked for 5s").
var TurnCompletionVerbs = []string{
	"Baked", "Brewed", "Churned", "Cogitated",
	"Cooked", "Crunched", "Sautéed", "Worked",
}
