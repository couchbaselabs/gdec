package gdec

import (
	"fmt"
	"strconv"
)

// Invoked by candidates to gather votes.
type RaftVoteReq struct {
	To           string
	From         string // Candidate requesting vote.
	Term         int    // Candidate's term.
	LastLogTerm  int    // Term of candidate's last log entry.
	LastLogIndex int    // Index of candidate's last log entry.
}

type RaftVoteRes struct { // Response.
	To      string
	From    string
	Term    int  // Current term, for candidate to update itself.
	Granted bool // True means candidate received vote.
}

// Invoked by leaders to replicate log entries.
type RaftAddEntryReq struct {
	To           string
	From         string // Leader's addr, allowing follower to redirect clients.
	Term         int    // Leader's term.
	PrevLogTerm  int    // Term of log entry immediately preceding this one.
	PrevLogIndex int    // Index of log entry immediately preceding this one.
	Entry        string // Log entry to store (empty for heartbeat).
	CommitIndex  int    // Last entry known to be commited.
}

type RaftAddEntryRes struct { // Response.
	To    string
	From  string
	Term  int  // Current term, for leader to update itself.
	Ok    bool // True if had entry matching PrevLogIndex/Term.
	Index int
}

type RaftVote struct {
	Term      int
	Candidate string
}

type RaftEntry struct {
	Term  int    // Term when entry was received by leader.
	Index int    // Position of entry in the log.
	Entry string // Command for state machine.
}

type RaftLogState struct {
	LastTerm        int
	LastIndex       int
	LastCommitIndex int
}

const (
	// The 'kind' of a state are in the lowest bits.
	state_FOLLOWER  = 0
	state_CANDIDATE = 1
	state_LEADER    = 2
	state_STEP_DOWN = 3 // Must be largest for LMax precedence.

	state_KIND_MASK    = 0x0000000f
	state_VERSION_MASK = 0xfffffff0 // Highest bits are version for precedence.
	state_VERSION_NEXT = 0x00000010
)

func stateKind(s int) int        { return s & state_KIND_MASK }
func stateVersion(s int) int     { return s & state_VERSION_MASK }
func stateVersionNext(s int) int { return stateVersion(s) + state_VERSION_NEXT }

func RaftProtocolInit(d *D, prefix string) *D {
	d.DeclareChannel(prefix+"RaftVoteReq", RaftVoteReq{})
	d.DeclareChannel(prefix+"RaftVoteRes", RaftVoteRes{})
	d.DeclareChannel(prefix+"RaftAddEntryReq", RaftAddEntryReq{})
	d.DeclareChannel(prefix+"RaftAddEntryRes", RaftAddEntryRes{})
	return d
}

func RaftInit(d *D, prefix string) *D {
	d = RaftProtocolInit(d, prefix)

	rvote := d.Relations[prefix+"RaftVoteReq"]
	rvoter := d.Relations[prefix+"RaftVoteRes"]

	radd := d.Relations[prefix+"RaftAddEntryReq"]
	raddr := d.Relations[prefix+"RaftAddEntryRes"]

	member := d.DeclareLSet(prefix+"raftMember", "addrString")

	curTerm := d.DeclareLMax(prefix + "raftCurTerm")
	curState := d.DeclareLMax(prefix + "raftCurState")

	nextTerm := d.Scratch(d.DeclareLMax(prefix + "raftNextTerm"))
	nextState := d.Scratch(d.DeclareLMax(prefix + "raftNextState"))

	alarm := d.Scratch(d.DeclareLBool(prefix + "raftAlarm"))           // TODO: periodic.
	alarmReset := d.Scratch(d.DeclareLBool(prefix + "raftAlarmReset")) // TODO: periodic.
	heartbeat := d.Scratch(d.DeclareLBool(prefix + "raftHeartbeat"))   // TODO: periodic.

	MultiTallyInit(d, prefix+"tallyLeader/")
	tallyLeaderVote := d.Relations[prefix+"tallyLeader/MultiTallyVote"].(*LSet)
	tallyLeaderNeed := d.Relations[prefix+"tallyLeader/MultiTallyNeed"].(*LMax)
	tallyLeaderDone := d.Relations[prefix+"tallyLeader/MultiTallyDone"].(*LMap)

	goodCandidate := d.Scratch(d.DeclareLSet(prefix+"raftGoodCandidate", RaftVoteReq{}))
	bestCandidate := d.Scratch(d.DeclareLMaxString(prefix + "raftBestCandidate"))

	// TODO: optimization to instead use LMap["term", LSet[RaftVote]].
	votedFor := d.DeclareLSet(prefix+"raftVotedFor", RaftVote{})
	votedForInCurTerm := d.Scratch(d.DeclareLSet(prefix+"raftVotedForInCurTerm", "addrString"))

	// Key: "index", val: LSet[RaftEntry].
	logEntry := d.DeclareLMap(prefix + "raftEntry")
	logState := d.DeclareLSet(prefix+"raftLogState", RaftLogState{}) // TODO: sub-module.
	logAdd := d.DeclareLSet(prefix+"raftLogAdd", RaftEntry{})        // TODO: sub-module.
	logCommit := d.DeclareLMax(prefix + "raftLogCommit")             // TODO: sub-module.

	nextIndex := d.DeclareLMap(prefix + "raftNextIndex") // Key: "addr", val: LMax.

	MultiTallyInit(d, prefix+"tallyCommit/")
	tallyCommitVote := d.Relations[prefix+"tallyCommit/MultiTallyVote"].(*LSet)
	tallyCommitNeed := d.Relations[prefix+"tallyCommit/MultiTallyNeed"].(*LMax)
	tallyCommitDone := d.Relations[prefix+"tallyCommit/MultiTallyDone"].(*LMap)

	// ------------------------------------------------------------------------

	d.Join(func() int { return member.Size() / 2 }).Into(tallyLeaderNeed)
	d.Join(func() int { return member.Size() / 2 }).Into(tallyCommitNeed)

	// Initialize our scratch next term/state.
	d.Join(curTerm).Into(nextTerm)
	d.Join(curState, func(s *int) int { return stateKind(*s) }).Into(nextState)

	// Incorporate next term and next state asynchronously.
	d.Join(nextTerm).IntoAsync(curTerm)
	d.Join(nextState, curState, func(n *int, s *int) int {
		if *n == state_STEP_DOWN {
			return stateVersionNext(*s) + state_FOLLOWER
		}
		return stateVersion(*s) + stateKind(*n)
	}).IntoAsync(curState)

	// Any incoming higher terms take precendence.
	d.Join(rvote, func(r *RaftVoteReq) int { return r.Term }).Into(nextTerm)
	d.Join(rvoter, func(r *RaftVoteRes) int { return r.Term }).Into(nextTerm)
	d.Join(radd, func(r *RaftAddEntryReq) int { return r.Term }).Into(nextTerm)
	d.Join(raddr, func(r *RaftAddEntryRes) int { return r.Term }).Into(nextTerm)

	// Any incoming higher terms can make us step down.
	d.Join(rvote, curTerm, curState,
		func(r *RaftVoteReq, t *int, s *int) int { return caseStepDown(r.Term, *t, *s) }).
		Into(nextState)
	d.Join(rvoter, curTerm, curState,
		func(r *RaftVoteRes, t *int, s *int) int { return caseStepDown(r.Term, *t, *s) }).
		Into(nextState)
	d.Join(radd, curTerm, curState,
		func(r *RaftAddEntryReq, t *int, s *int) int { return caseStepDown(r.Term, *t, *s) }).
		Into(nextState)
	d.Join(raddr, curTerm, curState,
		func(r *RaftAddEntryRes, t *int, s *int) int { return caseStepDown(r.Term, *t, *s) }).
		Into(nextState)

	// Timeout means we should become a candidate.
	d.Join(alarm, curTerm, curState, func(alarm *bool, t *int, s *int) {
		// Move to candidate state, with a new term, self-vote, and alarm reset.
		if *alarm && stateKind(*s) != state_LEADER {
			d.Add(nextTerm, *t+1)
			d.Add(nextState, state_CANDIDATE)
			d.Add(tallyLeaderVote, &MultiTallyVote{termToKey(*t + 1), d.Addr})
			// TODO: d.Add(resetAlarm, true)
			// TODO: remove uncommitted logs.
			return
		}
	})

	// Send vote requests.
	d.Join(heartbeat, member, curTerm, curState, logState,
		func(h *bool, a *string, t *int, s *int, l *RaftLogState) *RaftVoteReq {
			if stateKind(*s) == state_CANDIDATE &&
				!MultiTallyHasVoteFrom(d, prefix+"tallyLeader/", termToKey(*t), *a) {
				return &RaftVoteReq{To: *a, From: d.Addr, Term: *t,
					LastLogTerm: l.LastTerm, LastLogIndex: l.LastIndex}
			}
			return nil
		}).IntoAsync(rvote)

	// Tally votes when we're a candidate.
	d.Join(curTerm, curState, rvoter,
		func(curTerm *int, curState *int, r *RaftVoteRes) *MultiTallyVote {
			// Record granted vote if we're still a candidate in the same term.
			if stateKind(*curState) == state_CANDIDATE &&
				r.Term == *curTerm && r.Granted {
				return &MultiTallyVote{termToKey(r.Term), r.From}
			}
			return nil
		}).Into(tallyLeaderVote)

	d.Join(curTerm, curState,
		func(curTerm *int, curState *int) int {
			// Become leader if we won the race.
			if stateKind(*curState) == state_CANDIDATE {
				won := tallyLeaderDone.At(termToKey(*curTerm)).(*LBool)
				if won != nil && won.Bool() {
					return state_LEADER
				}
			}
			return stateKind(*curState)
		}).Into(nextState)

	// Cast votes.
	d.Join(curTerm, votedFor,
		func(curTerm *int, votedFor *RaftVote) *string {
			// Remember who we voted for in the current term.
			if *curTerm == votedFor.Term {
				return &votedFor.Candidate
			}
			return nil
		}).Into(votedForInCurTerm)

	d.Join(rvote, logState,
		func(rvote *RaftVoteReq, logState *RaftLogState) *RaftVoteReq {
			// Good candidate only if candidate's log is at or beyond our log.
			if rvote.LastLogTerm > logState.LastTerm ||
				(rvote.LastLogTerm == logState.LastTerm &&
					rvote.LastLogIndex >= logState.LastIndex) {
				return rvote
			}
			return nil
		}).Into(goodCandidate)

	d.Join(goodCandidate, func(g *RaftVoteReq) string { return g.From }).
		Into(bestCandidate) // Not the greatest best function, but it's stable.

	d.Join(rvote, bestCandidate, curTerm,
		func(r *RaftVoteReq, b *string, t *int) *RaftVoteRes {
			// Grant vote if we hadn't voted yet or if we already voted for the candidate.
			granted := r.Term >= *t &&
				((votedForInCurTerm.(*LSet).Size() == 0 && r.From == *b) ||
					(votedForInCurTerm.(*LSet).Contains(r.From)))
			return &RaftVoteRes{To: r.From, From: r.To, Term: *t, Granted: granted}
		}).IntoAsync(rvoter) // TODO: reset timer if we grant a vote to a candidate.

	d.Join(bestCandidate, curTerm,
		func(bestCandidate *string, curTerm *int) *RaftVote {
			// Remember our vote if we hadn't voted for anyone yet.
			if votedForInCurTerm.(*LSet).Size() == 0 && *bestCandidate != "" {
				return &RaftVote{*curTerm, *bestCandidate}
			}
			return nil
		}).IntoAsync(votedFor)

	// Send heartbeats.
	d.Join(heartbeat, member, curTerm, curState, logState,
		func(h *bool, a *string, t *int, s *int, l *RaftLogState) *RaftAddEntryReq {
			if stateKind(*s) != state_LEADER {
				return nil
			}
			return &RaftAddEntryReq{To: *a, From: d.Addr, Term: *t,
				PrevLogTerm: l.LastTerm, PrevLogIndex: l.LastIndex,
				Entry: "", CommitIndex: l.LastCommitIndex}
		}).IntoAsync(radd)

	// Handle add entry requests.
	d.Join(radd, curTerm,
		func(radd *RaftAddEntryReq, curTerm *int) bool {
			// Reset alarm if term is current or our term is stale.
			// TODO: random alarm timeout.
			return radd.Term >= *curTerm
		}).Into(alarmReset)

	d.Join(radd, curTerm, logState,
		func(r *RaftAddEntryReq, t *int, ls *RaftLogState) *RaftAddEntryRes {
			// Fail response if previous entry doesn't exist in our log.
			if r.PrevLogIndex <= ls.LastIndex {
				return nil
			}
			return &RaftAddEntryRes{To: r.From, From: r.To, Term: *t,
				Ok: false, Index: r.PrevLogIndex}
		}).IntoAsync(raddr)

	d.Join(radd, curState, logEntry, func(r *RaftAddEntryReq, s *int, m *LMapEntry) {
		// Send ok response only if log terms match.  And,
		// update entries if terms match, replacing/clearing later entries.
		if r.Entry == "" || stateKind(*s) == state_LEADER ||
			keyToIndex(m.Key) != r.PrevLogIndex {
			return
		}
		e := maxRaftEntry(m.Val.(*LSet))
		if e == nil {
			return
		}
		d.Add(raddr, &RaftAddEntryRes{To: r.From, From: r.To, Term: r.Term,
			Ok: r.PrevLogTerm == e.Term, Index: r.PrevLogIndex + 1})
		if r.PrevLogTerm == e.Term {
			d.Add(logAdd, &RaftEntry{
				Term: r.Term, Index: r.PrevLogIndex + 1, Entry: r.Entry})
		}
	})

	d.Join(radd, func(r *RaftAddEntryReq) int { return r.CommitIndex }).
		Into(logCommit) // TODO: commit entries before (or at?) this point?

	// Update followers.

	d.Join(heartbeat, curTerm, curState, logEntry, logState, nextIndex,
		func(h *bool, t *int, s *int,
			le *LMapEntry, ls *RaftLogState,
			n *LMapEntry) *RaftAddEntryReq {
			if !*h || stateKind(*s) != state_LEADER {
				return nil
			}
			e := maxRaftEntry(le.Val.(*LSet))
			if e == nil || e.Index != n.Val.(*LMax).Int()-1 {
				return nil
			}
			// TODO: feels like we don't get all the logs to the follower.
			return &RaftAddEntryReq{To: n.Key, From: d.Addr, Term: *t,
				PrevLogTerm: e.Term, PrevLogIndex: keyToIndex(le.Key),
				Entry: e.Entry, CommitIndex: ls.LastCommitIndex}
		}).IntoAsync(radd)

	d.Join(raddr, func(r *RaftAddEntryRes) *MultiTallyVote {
		if r.Ok {
			return &MultiTallyVote{indexToKey(r.Index), r.From}
		}
		return nil
	}).Into(tallyCommitVote)

	d.Join(tallyCommitDone, func(m *LMapEntry) int {
		if m.Val.(*LBool).Bool() {
			return keyToIndex(m.Key)
		}
		return 0
	}).Into(logCommit) // TODO: commit entries before (or at?) this point?

	// TODO: update nextIndex <+- (raddr * nextIndex) {|a,n|
	//    a.success? [a.from, i.index + 1] : [a.from, i.index - 1]}

	// TODO: send committed logs into the state machine to execute
	//    machine.execute <= logger.commited_logs

	return d
}

func init() {
	RaftInit(NewD(""), "")
}

func termToKey(term int) string   { return fmt.Sprintf("%d", term) }
func indexToKey(index int) string { return fmt.Sprintf("%d", index) }

func keyToIndex(key string) int {
	index, err := strconv.Atoi(key)
	if err != nil {
		return -1
	}
	return index
}

func caseStepDown(term, curTerm, curState int) int {
	if term > curTerm {
		return state_STEP_DOWN
	}
	return stateKind(curState)
}

func maxRaftEntry(entries *LSet) *RaftEntry {
	var max *RaftEntry
	for x := range entries.Scan() {
		e := x.(*RaftEntry)
		if max == nil ||
			(e.Term > max.Term) ||
			(e.Term == max.Term && e.Entry > max.Entry) {
			max = e
		}
	}
	return max
}
