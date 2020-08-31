// Copyright 2015 Comcast Cable Communications Management, LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// End Copyright

package core

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/robertkrimen/otto"
)

type App interface {
	GenerateHeaders(ctx *Context) map[string]string
	ProcessBindings(ctx *Context, bs Bindings) Bindings

	// UpdateJavascriptRuntime can be used to modify the
	// Javascript environment for actions and condition code.
	UpdateJavascriptRuntime(ctx *Context, runtime *otto.Otto) error
}

// Tracer can be used to perform application tracing.
type Tracer interface {
	// StartSpan is given a new subcontext and the name of the operation that
	// the context is for.  The function is expected to modify the context with
	// any span information that is needed to pass on to child spans as well as
	// to stop the span.
	StartSpan(ctx *Context, opName string)

	// StopSpan is given a context previously given to StartSpan in order to
	// finalize the trace for the given span.
	StopSpan(ctx *Context)
}

type Context struct {
	sync.RWMutex
	Ctx context.Context

	// Id is an unique-ish identifie that should be generated by a
	// constructor.
	id string

	Verbosity LogLevel

	// Try to avoid!
	location atomic.Value // *Location

	// If target location.Mode.ReadKey is not nil, then this
	// ReadKey must match it in order to a read API to be allowed.
	ReadKey string

	// If target location.Mode.WriteKey is not nil, then this
	// WriteKey must match it in order to a read API to be allowed.
	WriteKey string

	// LogAccumulator, if it exists, will collect log records.
	LogAccumulator *Accumulator

	// LogAccumulatorLimit determines LogAccumulator detail.
	LogAccumulatorLevel LogLevel

	// LogHook, if not nil, is called for every log record.
	LogHook LogHook

	// PointHook, if not nil, is called from 'Point()'.
	PointHook PointHook

	// App specific behavior modifiers
	App App

	// Custom application tracing
	Tracer Tracer

	// Functionality previous residing in csv-context-go/Context

	Logger   Logger
	props    map[string]interface{}
	logProps map[string]interface{}

	// privileged is a terrible, incomplete hack to grant state
	// access without a lock for state hooks (see AddHook and
	// RemHook).
	//
	// The problem is that a hook, which is defined outside of
	// this package, might need to do a operation that requires a
	// state lock.  Example: Location.Get().
	//
	// To implement this hack more completely, we should generate
	// and check unique keys that we hand out.
	privilege string
}

func (c *Context) SetLoc(loc *Location) *Location {
	c.location.Store(loc)
	return loc
}

func (c *Context) GetLoc() *Location {
	loc, ok := c.location.Load().(*Location)
	if ok {
		return loc
	}
	return nil
}

func (c *Context) Location() *Location {
	return c.GetLoc()
}

func (c *Context) Prop(prop string) interface{} {
	c.RLock()
	v := c.props[prop]
	c.RUnlock()
	return v
}

func (c *Context) AddProp(prop string, val interface{}) {
	c.Lock()
	c.props[prop] = val
	c.Unlock()
}

// SetValue just exists for backwards compatibility.  Just calls AddProp().
func (c *Context) SetValue(name string, val interface{}) {
	c.AddProp(name, val)
}

// AddValue just exists for backwards compatibility.  Just calls AddProp().
func (c *Context) AddValue(name string, val interface{}) {
	c.AddProp(name, val)
}

func (c *Context) Id() string {
	// c.id should have been set by a constructor (via init()), so
	// we don't need a mutex here.
	return c.id
}

func (c *Context) grantPrivilege(region string) {
	c.Lock()
	c.privilege = region
	c.Unlock()
}

func (c *Context) revokePrivilege() {
	c.Lock()
	c.privilege = ""
	c.Unlock()
}

func (c *Context) isPrivileged(region string) bool {
	c.RLock()
	is := c.privilege == region
	c.RUnlock()
	return is
}

// var NoContext = &Context{Context: context.NewContext()}
var NoContext = &Context{}

func NewContext(prefix string) *Context {
	return newContext(prefix)
}

func TestContext(prefix string) *Context {
	return newContext(prefix)
}

func (ctx *Context) init() *Context {
	ctx.id = UUID()
	if ctx.logProps == nil {
		ctx.logProps = make(map[string]interface{})
	}
	if ctx.props == nil {
		ctx.props = make(map[string]interface{})
	}
	return ctx
}

func newContext(appId string) *Context {
	ctx := &Context{RWMutex: sync.RWMutex{},
		Verbosity:           DefaultVerbosity,
		ReadKey:             "",
		WriteKey:            "",
		LogAccumulator:      nil,
		LogAccumulatorLevel: ANYWARN,
		LogHook:             nil,
		PointHook:           nil,
		Ctx:                 context.Background(),
	}
	ctx.init()
	// We really don't like gratuitous dots in properties because
	// they cause unncessary trouble in downstream processing.
	ctx.logProps["appId"] = appId
	return ctx
}

func (ctx *Context) SetLogValue(name string, val interface{}) {
	ctx.logProps[name] = val
}

func (ctx *Context) SubContext() *Context {
	ctx.RLock()

	sub := &Context{RWMutex: sync.RWMutex{},
		Verbosity:           ctx.Verbosity,
		ReadKey:             ctx.ReadKey,
		WriteKey:            ctx.WriteKey,
		LogAccumulator:      ctx.LogAccumulator,
		LogAccumulatorLevel: ctx.LogAccumulatorLevel,
		LogHook:             ctx.LogHook,
		PointHook:           ctx.PointHook,
		Logger:              ctx.Logger,
		App:                 ctx.App,
		privilege:           ctx.privilege,
		Ctx:                 ctx.Ctx,
		Tracer:              ctx.Tracer,
	}

	sub.SetLoc(ctx.GetLoc())

	sub.init()
	for p, v := range ctx.props {
		sub.props[p] = v
	}
	for p, v := range ctx.logProps {
		sub.logProps[p] = v
	}

	ctx.RUnlock()
	return sub
}

func (ctx *Context) StartSpan(opName string) *Context {
	ctx = ctx.SubContext()
	if ctx.Tracer != nil {
		ctx.Tracer.StartSpan(ctx, opName)
	}
	return ctx
}

func (ctx *Context) StopSpan() {
	if ctx.Tracer != nil {
		ctx.Tracer.StopSpan(ctx)
	}
}

func BenchContext(appId string) *Context {
	// ctx := context.NewContext(ioutil.Discard)
	// ctx.AddLogValue("app.id", prefix)
	ctx := newContext(appId)
	ctx.Verbosity = NOTHING
	ctx.Logger = BenchLogger
	ctx.LogAccumulatorLevel = NOTHING
	return ctx
}

func (ctx *Context) Log(level LogLevel, op string, args ...interface{}) {
	if ctx == nil || ctx.Logger == nil {
		DefaultLogger.Log(level, args)
	} else {
		ctx.Logger.Log(level, op, args)
	}
}
