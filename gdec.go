package gdec

import (
	"fmt"
	"reflect"
)

type D struct {
	Addr      string
	Relations map[string]Relation
	Joins     []*joinDeclaration
	ticks     int64
	next      []relationChange
	immediate []relationChange
}

type Relation interface {
	TupleType() reflect.Type

	// Used at declaration time, marks the relation as "scratch",
	// so it'll reset to zero at the start of each tick.
	DeclareScratch()

	// Invoked at the start of each tick.  Implementations marked as
	// scratch should reset to zero.
	startTick()

	// Used by the join algorithm when it needs an iterator over all
	// tuples in the relation.
	Scan() chan interface{}

	DirectAdd(tuple interface{}) bool // Returns true if Relation changed.
	DirectMerge(rel Relation) bool    // Returns true if Relation changed.
}

func NewD(addr string) *D {
	return &D{
		Addr:      addr,
		Relations: make(map[string]Relation),
		Joins:     []*joinDeclaration{},
		next:      []relationChange{},
		immediate: []relationChange{},
	}
}

func (d *D) DeclareChannel(name string, x interface{}) *LSet {
	c := d.DeclareLSet(name, x)
	c.DeclareScratch()
	c.channel = true
	return c
}

func (d *D) DeclareRelation(name string, x Relation) Relation {
	if d.Relations[name] != nil {
		panic(fmt.Sprintf("relation redeclared, name: %s"+
			", relation: %#v", name, x))
	}
	d.Relations[name] = x
	return x
}

func (d *D) Join(vars ...interface{}) *joinDeclaration {
	var r *Relation
	rt := reflect.TypeOf(r).Elem()

	var joinNum int
	var selectWhereFunc interface{}

	for i, x := range vars {
		if x == nil {
			panic("nil passed as Join() param")
		}
		xt := reflect.TypeOf(x)
		if xt.Kind() == reflect.Func {
			if i < len(vars)-1 {
				panic(fmt.Sprintf("func not last Join() param: %#v",
					vars))
			}
			selectWhereFunc = x
		} else if xt.Implements(rt) {
			joinNum = i + 1
		} else {
			panic(fmt.Sprintf("unexpected Join() param type: %#v, %v",
				x, xt))
		}
	}

	sources := make([]Relation, joinNum)
	for i := 0; i < joinNum; i++ {
		sources[i] = vars[i].(Relation)
	}

	if selectWhereFunc != nil {
		mft := reflect.TypeOf(selectWhereFunc)
		if mft.NumIn() != joinNum {
			panic(fmt.Sprintf("selectWhereFunc should take %v args"+
				", selectWhereFunc: %v", joinNum, mft))
		}
		for i, x := range sources {
			rt := reflect.PtrTo(x.TupleType())
			if rt != mft.In(i) {
				panic(fmt.Sprintf("selectWhereFunc param #%v type"+
					" %v does not match, expected: %v, selectWhereFunc: %v",
					i, mft.In(i), rt, mft))
			}
		}
	}

	jd := &joinDeclaration{
		d:               d,
		sources:         sources,
		selectWhereFunc: selectWhereFunc,
	}
	d.Joins = append(d.Joins, jd)
	return jd
}

func (d *D) JoinFlat(vars ...interface{}) *joinDeclaration {
	jd := d.Join(vars...)
	jd.selectWhereFlat = true
	return jd
}

func (d *D) Add(r Relation, v interface{}) {
	d.immediate = append(d.immediate, relationChange{r, v, true})
}

func (d *D) AddNext(r Relation, v interface{}) {
	d.next = append(d.next, relationChange{r, v, true})
}

func (d *D) Merge(r Relation, v interface{}) {
	d.immediate = append(d.immediate, relationChange{r, v, false})
}

func (d *D) MergeNext(r Relation, v interface{}) {
	d.next = append(d.next, relationChange{r, v, false})
}

type joinDeclaration struct {
	d               *D
	name            string
	sources         []Relation
	selectWhereFunc interface{}
	selectWhereFlat bool
	async           bool
	into            Relation
}

func (jd *joinDeclaration) Name(name string) *joinDeclaration {
	jd.name = name
	return jd
}

func (jd *joinDeclaration) IntoAsync(dest interface{}) *joinDeclaration {
	jd.async = true
	jd.Into(dest)
	return jd
}

func (jd *joinDeclaration) Into(dest interface{}) *joinDeclaration {
	var r *Relation
	rt := reflect.TypeOf(r).Elem()

	dt := reflect.TypeOf(dest)
	if !dt.Implements(rt) {
		panic(fmt.Sprintf("Into() param: %#v, type: %v"+
			", does not implement Relation", dest, dt))
	}

	jd.into = dest.(Relation)

	var out reflect.Type
	if jd.selectWhereFunc != nil {
		out = reflect.TypeOf(jd.selectWhereFunc).Out(0)
	} else if len(jd.sources) == 1 {
		out = reflect.PtrTo(jd.sources[0].TupleType())
	} else {
		panic(fmt.Sprintf("unexpected Into() join declaration: %#v", jd))
	}
	if jd.selectWhereFlat {
		if out != dt {
			panic(fmt.Sprintf("Into() param: %#v, type: %v, does not match"+
				" output type: %v", dest, dt, out))
		}
	} else {
		if out != jd.into.TupleType() &&
			out != reflect.PtrTo(jd.into.TupleType()) {
			panic(fmt.Sprintf("Into() param: %#v, type: %v, does not match"+
				" tuple type: %v", dest, dt, out))
		}
	}

	return jd
}

func (d *D) Scratch(r Relation) Relation { // Concise readability sugar.
	r.DeclareScratch()
	return r
}

func (d *D) Input(r Relation) Relation { // Concise readability sugar.
	r.DeclareScratch()
	return r
}

func (d *D) Output(r Relation) Relation { // Concise readability sugar.
	r.DeclareScratch()
	return r
}
