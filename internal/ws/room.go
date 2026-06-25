package ws

import (
	"log"
	"sync"
)

type Room struct {
	ID    string
	users map[string]*User
	mtx   sync.RWMutex
}

var (
	rooms    = make(map[string]*Room)
	roomsMtx sync.RWMutex
)

func GetOrCreateRoom(id string) *Room {
	roomsMtx.Lock()
	defer roomsMtx.Unlock()
	if r, ok := rooms[id]; ok {
		return r
	}

	r := &Room{
		ID:    id,
		users: make(map[string]*User),
	}
	rooms[id] = r
	log.Println("created room:", id)
	return r
}

func (r *Room) HasUser(id string) bool {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	_, ok := r.users[id]
	return ok
}

func (r *Room) AddUser(u *User) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	if _, exists := r.users[u.ID]; exists {
		return false
	}

	r.users[u.ID] = u
	u.room = r
	log.Printf("user \"%s\" joined room %s (now %d users)\n", u.DisplayName, r.ID, len(r.users))
	return true
}

func (r *Room) RemoveUser(u *User) {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	delete(r.users, u.ID)
	u.room = nil
	log.Printf("user \"%s\" left room %s (now %d users)\n", u.DisplayName, r.ID, len(r.users))

	if len(r.users) == 0 {
		roomsMtx.Lock()
		delete(rooms, r.ID)
		roomsMtx.Unlock()
		log.Printf("room %s removed (empty)\n", r.ID)
	}
}

func (r *Room) IterateUsers(fn func(u *User)) {
	r.mtx.RLock()
	urs := make([]*User, 0, len(r.users))
	for _, u := range r.users {
		urs = append(urs, u)
	}
	r.mtx.RUnlock()

	for _, u := range urs {
		fn(u)
	}
}
