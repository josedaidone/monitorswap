package main

// swapOutcome describes what happened when a swap was requested on this PC.
type swapOutcome struct {
	NewLocal   Mode
	Reached    bool  // the other PC confirmed its half
	Err        error // this PC could not switch
	HandedBack bool  // this PC could not take the monitor, so the other PC was told to keep it
}

// runSwap carries out a hotkey press / menu choice / command.
//
//	kind:  "swap" (toggle), "take" (monitor to this PC) or "give" (monitor to the other PC)
//	local: this PC's current mode
//	send:  asks the other PC to switch; false if it is unreachable
//	apply: switches this PC
//
// The PC where the request was made decides for both and tells the other one
// exactly which mode to use, so the two can never drift into both-extended
// or both-released. If the other PC is unreachable, this PC switches anyway.
func runSwap(kind string, local Mode, send func(Mode) bool, apply func(Mode) error) swapOutcome {
	var out swapOutcome
	switch kind {
	case "take":
		out.NewLocal = ModeExtend
	case "give":
		out.NewLocal = ModeInternal
	default:
		out.NewLocal, _ = planSwap(local)
	}
	out.Reached = send(out.NewLocal.opposite())
	out.Err = apply(out.NewLocal)
	if out.Err == errSuperseded {
		out.Err = nil
	}
	if out.Err != nil && out.NewLocal == ModeExtend && out.Reached {
		// This PC cannot see the monitor: hand it back rather than leave it
		// dark on both.
		out.HandedBack = send(ModeExtend)
	}
	return out
}
