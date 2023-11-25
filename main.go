package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

const (
	MessageTypeNewUser         = "NewUser"
	MessageTypeChat            = "Chat"
	MessageTypeLeave           = "Leave"
	MessageTypePrivateRoomFull = "PrivateRoomFull"
)

var (
	connections = make(map[string]map[*WebSocketConnection]struct{})
	mutex       sync.Mutex
)

type SocketPayload struct {
	Message string `json:"message"`
}

type SocketResponse struct {
	From    string `json:"from"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type WebSocketConnection struct {
	*websocket.Conn
	Username string
	Room     string
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		content, err := os.ReadFile("index.html")
		if err != nil {
			http.Error(w, "Faiilll", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "%s", content)
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		currentGorillaConn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
		if err != nil {
			http.Error(w, "Fail websocket", http.StatusBadRequest)
			return
		}

		username := r.URL.Query().Get("username")
		room := r.URL.Query().Get("room")

		mutex.Lock()
		if connections[room] == nil {
			connections[room] = make(map[*WebSocketConnection]struct{})
		}

		if len(connections[room]) >= getMaxParticipants(room) {
			mutex.Unlock()
			log.Printf("%s room full.\n", room)
			return
		}

		currentConn := WebSocketConnection{Conn: currentGorillaConn, Username: username, Room: room}
		connections[room][&currentConn] = struct{}{}
		mutex.Unlock()

		go handleIO(&currentConn, room)
	})

	fmt.Println("Server starting at :8080")
	http.ListenAndServe(":8080", nil)
}
func getMaxParticipants(roomType string) int {
	if roomType == "private" {
		return 2
	} else if roomType == "group" {
		return 5
	}
	return 0
}

func handleIO(currentConn *WebSocketConnection, room string) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("ERROR", fmt.Sprintf("%v", r))
		}
	}()

	mutex.Lock()
	roomConnections := make([]*WebSocketConnection, 0, len(connections[room]))
	for conn := range connections[room] {
		roomConnections = append(roomConnections, conn)
	}
	mutex.Unlock()

	currentConn.WriteJSON(SocketResponse{
		From:    "Server",
		Type:    MessageTypeChat,
		Message: "Welcome to the chat!",
	})

	messageChan := make(chan string, 256)
	go func() {
		for message := range messageChan {
			mutex.Lock()
			roomConnections := make([]*WebSocketConnection, 0, len(connections[room]))
			for conn := range connections[room] {
				roomConnections = append(roomConnections, conn)
			}
			mutex.Unlock()

			broadcastMessage(currentConn, MessageTypeChat, roomConnections, message)
		}
	}()

	for {
		payload := SocketPayload{}
		err := currentConn.ReadJSON(&payload)
		if err != nil {
			if strings.Contains(err.Error(), "websocket: close") {
				close(messageChan)
				broadcastMessage(currentConn, MessageTypeLeave, roomConnections, currentConn.Username+" has left the room.")
				ejectConnection(currentConn, room)
				return
			}

			log.Println("ERROR", err.Error())
			continue
		}

		messageChan <- payload.Message
	}
}

func ejectConnection(currentConn *WebSocketConnection, room string) {
	mutex.Lock()
	delete(connections[room], currentConn)
	roomConnections := make([]*WebSocketConnection, 0, len(connections[room]))
	for conn := range connections[room] {
		roomConnections = append(roomConnections, conn)
	}
	mutex.Unlock()

	broadcastMessage(currentConn, MessageTypeLeave, roomConnections, "")
}

func broadcastMessage(currentConn *WebSocketConnection, kind string, roomConnections []*WebSocketConnection, message string) {
	mutex.Lock()
	defer mutex.Unlock()

	for _, eachConn := range roomConnections {
		if eachConn != currentConn {
			go func(conn *WebSocketConnection) {
				err := conn.WriteJSON(SocketResponse{
					From:    currentConn.Username,
					Type:    kind,
					Message: message,
				})
				if err != nil {
					log.Println("ERROR", err.Error())
				}
			}(eachConn)
		}
	}
}
