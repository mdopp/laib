package state

type Hand struct {
	Ceiling string
	Verbs   []string
}

const DefaultPolicy = "tiers: [read, propose, apply]\nhands:\n  ops: {ceiling: apply}\n  human: {ceiling: apply}\nautonomy:\n  values: auto\n"

var HelperHand = Hand{Ceiling: "propose", Verbs: []string{"propose", "note"}}
