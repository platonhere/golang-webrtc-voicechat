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
	rooms   = make(map[string]*Room)
	roomsMu sync.RWMutex
)

func GetOrCreateRoom(id string) *Room {
	roomsMu.Lock()
	defer roomsMu.Unlock()
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

func (r *Room) AddUser(u *User) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.users[u.ID] = u
	u.room = r
	log.Printf("user %s joined room %s (now %d users)\n", u.ID, r.ID, len(r.users))
}

func (r *Room) RemoveUser(u *User) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	delete(r.users, u.ID)
	u.room = nil
	log.Printf("user %s left room %s (now %d users)\n", u.ID, r.ID, len(r.users))
	if len(r.users) == 0 {
		roomsMu.Lock()
		delete(rooms, r.ID)
		roomsMu.Unlock()
		log.Printf("room %s removed (empty)\n", r.ID)
	}
}

func (r *Room) IterateUsers(fn func(u *User)) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	for _, u := range r.users {
		fn(u)
	}
}
