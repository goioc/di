/*
 * Copyright (c) 2024 Go IoC
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 */

package di

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sort"
	"sync"

	"github.com/sirupsen/logrus"
)

type lifecycleState uint8

const (
	uninitialized lifecycleState = iota
	initializing
	initialized
	rollingBack
	closing
)

func acquireContainer() (*container, func(), error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != initializing && containerState != initialized {
		return nil, nil, errors.New("container is not initialized: can't lookup instances of beans yet")
	}
	activeLookups++
	return runningContainer, func() {
		initializeShutdownLock.Lock()
		activeLookups--
		lifecycleChanged.Broadcast()
		initializeShutdownLock.Unlock()
	}, nil
}

type beanState uint8

const (
	allocated beanState = iota
	wiring
	wired
	initializingBean
	ready
	failed
)

type beanNode struct {
	id           string
	scope        Scope
	instance     interface{}
	ctx          context.Context
	dependencies []*beanNode
	provided     bool
	state        beanState
	err          error
	closed       bool
}

// Registration maps are frozen for the lifetime of this container. Only node
// bookkeeping needs a lock, and user code is never invoked while it is held.
type container struct {
	beans          map[string]reflect.Type
	factories      map[string]func(context.Context) (interface{}, error)
	scopes         map[string]Scope
	postprocessors map[reflect.Type][]func(interface{}) error
	mu             sync.Mutex
	singletons     map[string]*beanNode
	starting       bool
	created        []*beanNode
	completed      []*beanNode
	startupClosed  map[interface{}]bool
	provided       map[interface{}]bool
}

// Called under initializeShutdownLock. Registration is immutable until reset,
// which replaces these maps instead of modifying the captured maps.
func newContainer() *container {
	c := &container{
		beans: beans, factories: beanFactories, scopes: scopes,
		postprocessors: beanPostprocessors, singletons: make(map[string]*beanNode),
		starting: true, startupClosed: make(map[interface{}]bool), provided: make(map[interface{}]bool),
	}
	for _, id := range c.ids(Singleton) {
		n := &beanNode{id: id, scope: Singleton, ctx: context.Background(), provided: userCreatedInstances[id]}
		if n.provided {
			n.instance = singletonInstances[id]
			c.provided[n.instance] = true
		}
		c.singletons[id] = n
		c.created = append(c.created, n)
	}
	return c
}

func (c *container) ids(scope Scope) []string {
	var ids []string
	for id, s := range c.scopes {
		if s == scope {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// A resolution owns its newly allocated prototypes/request beans until success.
// Startup additionally owns all container-created beans until the attempt commits.
type resolution struct {
	created []*beanNode
}

func (c *container) resolve(ctx context.Context, id string) (instance interface{}, err error) {
	r := &resolution{}
	var cancelRequest context.CancelFunc
	if c.scopes[id] == Request {
		// A failed request must stop its context-bound work before its closers run.
		// On success the middleware-owned parent controls this context's lifetime.
		ctx, cancelRequest = context.WithCancel(ctx)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			if cancelRequest != nil {
				cancelRequest()
			}
			c.closeNodes(r.created, false, false)
		}
	}()
	n, err := c.build(ctx, id, r, make(map[string]bool))
	if err != nil {
		return nil, err
	}
	if err = c.initialize(n, make(map[*beanNode]bool)); err != nil {
		return nil, err
	}
	succeeded = true
	return n.instance, nil
}

func (c *container) constructionNode(ctx context.Context, id string, r *resolution, path map[string]bool) (n *beanNode, existing bool, err error) {
	scope, found := c.scopes[id]
	if !found {
		return nil, false, errors.New("bean is not registered: " + id)
	}
	c.mu.Lock()
	if scope == Singleton {
		n = c.singletons[id]
		if n.state == failed {
			c.mu.Unlock()
			return nil, false, n.err
		}
		if n.state >= wired {
			c.mu.Unlock()
			return n, true, nil
		}
		if n.state == wiring {
			instance := n.instance
			c.mu.Unlock()
			if path[id] && instance != nil {
				// Preallocated singleton references permit field-injection cycles.
				return n, true, nil
			}
			return nil, false, errors.New("bean construction is already in progress: " + id)
		}
		n.state = wiring
	} else {
		if path[id] {
			c.mu.Unlock()
			return nil, false, errors.New("circular dependency detected for bean: " + id)
		}
		n = &beanNode{id: id, scope: scope, ctx: ctx, state: wiring}
		r.created = append(r.created, n)
		if c.starting {
			c.created = append(c.created, n)
		}
	}
	c.mu.Unlock()
	return n, false, nil
}

func (c *container) build(ctx context.Context, id string, r *resolution, path map[string]bool) (n *beanNode, err error) {
	n, existing, err := c.constructionNode(ctx, id, r, path)
	if err != nil || existing {
		return n, err
	}
	path[id] = true
	defer delete(path, id)
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			n.state, n.err = failed, err
		}
	}()
	if n.instance == nil {
		var value interface{}
		if factory := c.factories[id]; factory != nil {
			value, err = factory(ctx)
			if err != nil {
				return n, err
			}
			if err = validateFactoryInstance(value); err != nil {
				return n, err
			}
		} else {
			value = reflect.New(c.beans[id].Elem()).Interface()
		}
		c.mu.Lock()
		n.instance = value
		c.mu.Unlock()
	}
	if !n.provided && c.factories[id] == nil {
		err = c.injectDependencies(id, n.instance, func(dependency string) (interface{}, error) {
			dep, buildErr := c.build(context.Background(), dependency, r, path)
			if buildErr != nil {
				return nil, buildErr
			}
			n.dependencies = append(n.dependencies, dep)
			return dep.instance, nil
		})
		if err != nil {
			return n, err
		}
	}
	c.mu.Lock()
	n.state = wired
	c.mu.Unlock()
	return n, nil
}

func (c *container) initialize(n *beanNode, path map[*beanNode]bool) (err error) {
	c.mu.Lock()
	if n.state == ready || n.state == initializingBean || path[n] {
		// Preserve singleton lookup from its own hook (or a singleton cycle).
		// Applications must wait for startup before sharing these instances.
		c.mu.Unlock()
		return nil
	}
	if n.state == failed {
		c.mu.Unlock()
		return n.err
	}
	if n.state != wired {
		c.mu.Unlock()
		return errors.New("bean initialization is already in progress: " + n.id)
	}
	n.state = initializingBean
	c.mu.Unlock()
	path[n] = true
	defer delete(path, n)
	defer func() {
		if err != nil {
			c.mu.Lock()
			n.state, n.err = failed, err
			c.mu.Unlock()
		}
	}()
	for _, dep := range n.dependencies {
		if err = c.initialize(dep, path); err != nil {
			return err
		}
	}
	if err = c.initializeInstance(n.ctx, n.id, n.instance); err != nil {
		return err
	}
	c.mu.Lock()
	n.state = ready
	if n.scope == Singleton || c.starting {
		c.completed = append(c.completed, n)
	}
	c.mu.Unlock()
	return nil
}

func (c *container) finishInitialization() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.ids(Singleton) {
		n := c.singletons[id]
		if n.err != nil {
			return n.err
		}
		if n.state != ready {
			return errors.New("singleton initialization did not complete: " + id)
		}
	}
	c.starting = false
	c.created = nil
	c.startupClosed = nil
	var singletons []*beanNode
	for _, n := range c.completed {
		if n.scope == Singleton {
			singletons = append(singletons, n)
		}
	}
	c.completed = singletons
	return nil
}

func (c *container) rollback() {
	nodes := append([]*beanNode(nil), c.completed...)
	nodes = append(nodes, c.created...)
	c.closeNodes(nodes, true, false)
}

func (c *container) closeSingletons() {
	// Completion order already captures dependencies, including callback lookups
	// and singleton cycles. A second graph traversal can reorder cycle members.
	nodes := c.completed
	if len(nodes) == 0 {
		// Close can also dispose of supplied instances before initialization.
		for _, id := range c.ids(Singleton) {
			nodes = append(nodes, c.singletons[id])
		}
	}
	c.closeOrderedNodes(nodes, true, true)
}

// dependencyOrder visits dependencies before their owners and tolerates singleton cycles.
func dependencyOrder(nodes []*beanNode) []*beanNode {
	allowed := make(map[*beanNode]bool)
	for _, n := range nodes {
		allowed[n] = true
	}
	seen := make(map[*beanNode]bool)
	var ordered []*beanNode
	var visit func(*beanNode)
	visit = func(n *beanNode) {
		if seen[n] || !allowed[n] {
			return
		}
		seen[n] = true
		for _, dep := range n.dependencies {
			visit(dep)
		}
		ordered = append(ordered, n)
	}
	for _, n := range nodes {
		visit(n)
	}
	return ordered
}

func (c *container) closeNodes(nodes []*beanNode, includeSingletons, includeProvided bool) {
	c.closeOrderedNodes(dependencyOrder(nodes), includeSingletons, includeProvided)
}

func (c *container) closeOrderedNodes(ordered []*beanNode, includeSingletons, includeProvided bool) {
	closed := make(map[interface{}]bool)
	for i := len(ordered) - 1; i >= 0; i-- {
		n := ordered[i]
		if value := c.claimCleanup(n, includeSingletons, includeProvided, closed); value != nil {
			closeBean(n.id, value)
		}
	}
}

// Distinct allocations of zero-size Go types may have equal pointers.
func hasInstanceIdentity(value interface{}) bool {
	return value != nil && reflect.TypeOf(value).Elem().Size() != 0
}

// Called with c.mu held. A failed resolution must not dispose of an existing singleton.
func (c *container) preserveInstance(n *beanNode, includeSingletons, includeProvided bool) bool {
	// Equal zero-size pointers may be aliases or distinct allocations. Prefer
	// retaining an existing owner's instance when that distinction is ambiguous.
	if !includeProvided && (n.provided || c.provided[n.instance]) {
		return true
	}
	if includeSingletons {
		return false
	}
	if n.scope == Singleton {
		return true
	}
	for _, singleton := range c.singletons {
		if singleton.instance == n.instance {
			return true
		}
	}
	return false
}

func (c *container) claimCleanup(n *beanNode, includeSingletons, includeProvided bool, closed map[interface{}]bool) interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	value := n.instance
	if value == nil || n.closed || c.preserveInstance(n, includeSingletons, includeProvided) {
		return nil
	}
	// Results equal to a supplied instance share that instance's ownership,
	// including zero-size pointers. Other zero-size allocations remain separate.
	deduplicate := hasInstanceIdentity(value) || c.provided[value]
	if deduplicate && (closed[value] || c.startupClosed[value]) {
		return nil
	}
	n.closed = true
	closed[value] = deduplicate
	if c.starting && deduplicate {
		c.startupClosed[value] = true
	}
	return value
}

func closeBean(id string, instance interface{}) {
	if closer, ok := instance.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			logrus.WithField("beanID", id).Error(err)
		}
	}
}
