package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

const (
	JOIN         = "JOIN"
	LEAVE        = "LEAVE"
	ADD_COMMENT  = "ADD_COMMENT"
	CHANGE_TITLE = "CHANGE_TITLE"
	ROOM_DATA    = "ROOM_DATA"

	// events
	COMMENT_ADDED = "COMMENT_ADDED"
	USER_JOINED   = "USER_JOINED"
	USER_LEFT     = "USER_LEFT"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func getMessage(
	messageType string, payload map[string]interface{},
) map[string]interface{} {
	return map[string]interface{}{
		"type":    messageType,
		"payload": payload,
	}
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	defer conn.Close()

	session, err := s.RediStore.Get(r, sessionPrefix)
	if err != nil {
		handleInternalServerError(err, w)
		return
	}

	userId := ""
	if session.Values[tokenCredKey] != nil {
		userId = session.Values[usernameKey].(string)
	}

	// Only one room available per connection.
	currentRoomId := ""
	var roomMessages chan StateMessage

	leave := func() {
		if currentRoomId != "" {
			s.State.Leave(currentRoomId, roomMessages, userId)
		}
	}
	defer leave()

	// Receive messages
	for {
		var message StateMessage
		if err := conn.ReadJSON(&message); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
				log.Println("read error:", err)
			}
			return
		}

		switch message.Type {
		case JOIN:
			leave()
			currentRoomId = message.Payload.(string)
			room := s.State.Join(currentRoomId, roomMessages, userId)

			// Send the initial payload on join.
			roomData := getMessage(ROOM_DATA, room.ToJSON())
			if err = conn.WriteJSON(roomData); err != nil {
				log.Println("join error:", err)
				return
			}

			roomMessages = s.State.SubscribeRoom(currentRoomId)

			go func() {
				for {
					message := <-roomMessages
					if message.End {
						return
					}

					err = conn.WriteJSON(map[string]interface{}{
						"payload": message.Payload,
						"type":    message.Type,
					})
					if err != nil {
						log.Println("send error:", err)
					}
				}
			}()

		case LEAVE:
			leave()

		case ADD_COMMENT:
			// Checks that:
			// 1. A room is already loaded.
			// 2. The user is authenticated.
			if currentRoomId != "" && userId != "" {
				s.State.AddComment(currentRoomId, roomMessages, Comment{
					SenderId: userId,
					Text:     message.Payload.(string),
				})
			}

		case CHANGE_TITLE:
			// Checks that:
			// 1. A room is already loaded.
			// 2. The user is authenticated.
			// 3. The user is the host.
			if currentRoomId != "" && userId != "" && currentRoomId == userId {
				title := message.Payload.(string)
				s.State.ChangeRoomTitle(currentRoomId, roomMessages, title)
			}
		}
	}
}
