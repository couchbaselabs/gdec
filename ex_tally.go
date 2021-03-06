package gdec

// Simple vote tally/counter.
func TallyInit(d *D, prefix string) *D {
	tvote := d.Input(d.DeclareLSet(prefix+"TallyVote", "voterString"))
	tneed := d.DeclareLMax(prefix + "TallyNeed")
	tdone := d.Output(d.DeclareLBool(prefix + "TallyDone"))

	ttotal := d.DeclareLSet(prefix+"tallyTotal", "voterString")

	d.Join(tvote).Into(ttotal)
	d.Join(func() bool { return ttotal.Size() >= tneed.Int() }).Into(tdone)

	return d
}

func init() {
	TallyInit(NewD(""), "")
}

type MultiTallyVote struct {
	Race  string
	Voter string
}

// Multiple tally/counters, when there are multiple, in-flight races (or contests).
func MultiTallyInit(d *D, prefix string) *D {
	tvote := d.Input(d.DeclareLSet(prefix+"MultiTallyVote", MultiTallyVote{}))
	tneed := d.DeclareLMax(prefix + "MultiTallyNeed")
	tdone := d.Output(d.DeclareLMap(prefix + "MultiTallyDone")) // Key: raceStr, val: LBool.

	ttotal := d.DeclareLMap(prefix + "multiTallyTotal") // Key: raceStr, val: LSet[voterStr].

	d.Join(tvote, func(tvote *MultiTallyVote) *LMapEntry {
		return &LMapEntry{tvote.Race, NewLSetOne(d, tvote.Voter)}
	}).Into(ttotal)

	d.Join(ttotal, func(m *LMapEntry) *LMapEntry {
		if m.Val.(*LSet).Size() >= tneed.Int() {
			return &LMapEntry{m.Key, NewLBool(d, true)}
		}
		return &LMapEntry{m.Key, NewLBool(d, false)}
	}).Into(tdone)

	return d
}

func init() {
	MultiTallyInit(NewD(""), "")
}

func MultiTallyVoters(d *D, prefix string, race string) *LSet {
	return d.Relations[prefix+"multiTallyTotal"].(*LMap).At(race).(*LSet)
}

func MultiTallyHasVoteFrom(d *D, prefix string, race string, voter string) bool {
	s := MultiTallyVoters(d, prefix, race)
	if s != nil {
		return s.Contains(voter)
	}
	return false
}
