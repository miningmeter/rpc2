package rpc2

import "sync"

/*
State - state of connection.
*/
type State struct {
	store map[string]interface{}
	m     sync.RWMutex
}

/*
NewState - connection state initialization.
*/
func NewState() *State {
	return &State{store: make(map[string]interface{})}
}

/*
Get - get connection state variable.
*/
func (s *State) Get(key string) (value interface{}, ok bool) {
	s.m.RLock()
	value, ok = s.store[key]
	s.m.RUnlock()
	return
}

/*
Set - set connection state variable.
*/
func (s *State) Set(key string, value interface{}) {
	s.m.Lock()
	s.store[key] = value
	s.m.Unlock()
}
