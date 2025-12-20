package ws

import (
	"log"
	"sync"
)

// экземпляр комнаты, хранит подключенных юзеров
type Room struct {
	ID    string
	users map[string]*User
	mtx   sync.RWMutex
}

var (
	// глобальная таблица комнат, для потокобезопасного доступа ко всей карте используется один общий мьютекс
	rooms    = make(map[string]*Room)
	roomsMtx sync.RWMutex
)

// GetOrCreateRoom возвращает существующую комнату или создаёт новую.
func GetOrCreateRoom(id string) *Room {
	// исп. lock вместо rlock, тк может произойти создание комнаты
	roomsMtx.Lock()
	defer roomsMtx.Unlock()
	if r, ok := rooms[id]; ok {
		return r
	}
	// если комнаты нет, создаем
	r := &Room{
		ID:    id,
		users: make(map[string]*User),
	}
	// заносим комнату по id в мапу
	rooms[id] = r
	log.Println("created room:", id)
	return r
}

// HasUser сообщает, находится ли пользователь с заданным id в комнате
// используется для предварительной проверки перед добавлением
func (r *Room) HasUser(id string) bool {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	_, ok := r.users[id]
	return ok
}

// AddUser пытается добавить пользователя в комнату (атомарно благодаря mtx)
func (r *Room) AddUser(u *User) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	// проверяем что юзера еще нет в мапе юзеров этой комнаты
	if _, exists := r.users[u.ID]; exists {
		return false
	}
	// добавляем пользователя в мапу юзеров по id
	r.users[u.ID] = u
	// присваеваем ему комнату, в которой находиться
	u.room = r
	log.Printf("user \"%s\" joined room %s (now %d users)\n", u.DisplayName, r.ID, len(r.users))
	return true
}

// RemoveUser удаляет пользователя из комнаты и при пустой комнате удаляет
// саму комнату из глобальной таблицы rooms.
func (r *Room) RemoveUser(u *User) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	// удаляет юзера из мапы юзеров комнаты по id
	delete(r.users, u.ID)
	// у юзера обнуляет комнату
	u.room = nil
	log.Printf("user \"%s\" left room %s (now %d users)\n", u.DisplayName, r.ID, len(r.users))
	// если в комнате 0 юзеров - удаляем комнату
	if len(r.users) == 0 {
		roomsMtx.Lock()
		// удаляем room из глобальной мапы
		delete(rooms, r.ID)
		roomsMtx.Unlock()
		log.Printf("room %s removed (empty)\n", r.ID)
	}
}

// IterateUsers создаёт "снимок" пользователей под RLock в текущий момент и вызывает
// callback без удержания блокировки, чтобы не блокировать конкурентные операции (добавление/удаление пользователей)
func (r *Room) IterateUsers(fn func(u *User)) {
	// rlock, тк мы просто читаем из r.users
	r.mtx.RLock()
	// создаем слайс *User'ов, в который добав. юзеров из комнаты (в тек. мом.)
	urs := make([]*User, 0, len(r.users))
	for _, u := range r.users {
		urs = append(urs, u)
	}
	r.mtx.RUnlock()

	// для каждого выполняем фун-ю
	for _, u := range urs {
		fn(u)
	}
}
