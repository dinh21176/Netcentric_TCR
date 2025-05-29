package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	conn      net.Conn
	username  string
	clientKey string
	roomID    int
	mana      int
	inputCh   chan string
	botLevel  int
	gameMode  string
	ready     bool // Add ready flag for replay
}

type Troop struct {
	player    int
	troopType int
	lane      string
	position  int
	age       int
	alive     bool
}

type Room struct {
	id       int
	clients  [2]*Client
	troops   []*Troop
	towerHP  map[int]map[string]int
	mu       sync.Mutex
	doneChan chan struct{}
}

var (
	clients     = make(map[string]*Client)
	rooms       = make(map[int]*Room)
	waitingRoom = make(chan *Client, 100)
	clientCount = 0
	roomCount   = 0
	globalMu    sync.Mutex
)

func main() {
	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
	defer ln.Close()

	fmt.Println("Server listening on port 8080...")

	go matchPlayers()

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Accept error:", err)
			continue
		}
		go handleConnection(conn)
	}
}

func resetRoom(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()

	room.towerHP[1] = map[string]int{"L": 100, "C": 100, "R": 100}
	room.towerHP[2] = map[string]int{"L": 100, "C": 100, "R": 100}
	room.troops = []*Troop{}

	for _, client := range room.clients {
		if client != nil {
			client.mana = 0
			client.ready = false // Reset ready flag
		}
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	authLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("Read auth error:", err)
		return
	}
	authLine = strings.TrimSpace(authLine)
	parts := strings.Split(authLine, ":")
	if len(parts) != 2 {
		conn.Write([]byte("Invalid auth format. Use username:password\n"))
		return
	}
	username := parts[0]
	password := parts[1]
	if username == "" || password == "" {
		conn.Write([]byte("Invalid credentials\n"))
		return
	}

	globalMu.Lock()
	clientCount++
	clientKey := fmt.Sprintf("C%d", clientCount)
	client := &Client{
		conn:      conn,
		username:  username,
		clientKey: clientKey,
		mana:      0,
		inputCh:   make(chan string, 10),
		botLevel:  0,
		gameMode:  "",
		ready:     false,
	}
	clients[clientKey] = client
	globalMu.Unlock()

	conn.Write([]byte(fmt.Sprintf("%s_Authenticated.\nChoose mode:\n1. Play vs Bot\n2. Play vs Player\n", clientKey)))

	modeLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("Read mode error:", err)
		return
	}
	mode := strings.TrimSpace(modeLine)

	switch mode {
	case "1":
		conn.Write([]byte("Chọn độ khó:\n1. Dễ\n2. Vừa\n3. Khó\n"))
		levelLine, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Read level error:", err)
			return
		}
		levelLine = strings.TrimSpace(levelLine)
		level, err := strconv.Atoi(levelLine)
		if err != nil || level < 1 || level > 3 {
			conn.Write([]byte("Level không hợp lệ. Ngắt kết nối.\n"))
			return
		}
		client.gameMode = "bot"
		go startBotGame(client, level)

	case "2":
		conn.Write([]byte("Waiting for another player...\n"))
		client.gameMode = "pvp"
		waitingRoom <- client

	default:
		conn.Write([]byte("Invalid mode. Disconnecting.\n"))
		return
	}

	go listenClientInput(client)

	for {
		select {
		case <-time.After(10 * time.Minute):
			conn.Write([]byte("Session timeout. Disconnecting.\n"))
			return
		}
	}
}

func startBotGame(p1 *Client, level int) {
	globalMu.Lock()
	roomCount++
	roomID := roomCount
	globalMu.Unlock()

	bot := &Client{
		username:  fmt.Sprintf("BotLv%d", level),
		clientKey: "Bot",
		mana:      0,
		inputCh:   make(chan string, 10),
		botLevel:  level,
		gameMode:  "bot",
		ready:     false,
	}

	room := &Room{
		id:       roomID,
		clients:  [2]*Client{p1, bot},
		troops:   []*Troop{},
		towerHP:  make(map[int]map[string]int),
		doneChan: make(chan struct{}),
	}
	room.towerHP[1] = map[string]int{"L": 100, "C": 100, "R": 100}
	room.towerHP[2] = map[string]int{"L": 100, "C": 100, "R": 100}
	p1.roomID = roomID
	bot.roomID = roomID
	rooms[roomID] = room

	go func() {
		for {
			select {
			case <-room.doneChan:
				return
			default:
				var delay time.Duration
				switch level {
				case 1:
					delay = 7 * time.Second
				case 2:
					delay = 4 * time.Second
				case 3:
					delay = 2 * time.Second
				}
				time.Sleep(delay)

				room.mu.Lock()
				if bot.mana >= 5 {
					var command string
					switch level {
					case 1:
						command = "1-L"
					case 2:
						command = "2-C"
					case 3:
						lanes := []string{"L", "C", "R"}
						types := []string{"1", "2", "3"}
						command = fmt.Sprintf("%s-%s", types[rand.Intn(len(types))], lanes[rand.Intn(len(lanes))])
					}
					bot.inputCh <- command
				}
				room.mu.Unlock()
			}
		}
	}()

	go gameLoop(room)
}

func listenClientInput(client *Client) {
	reader := bufio.NewReader(client.conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Client disconnected:", client.clientKey)
			close(client.inputCh)
			return
		}
		line = strings.TrimSpace(line)
		client.inputCh <- line
	}
}

func matchPlayers() {
	for {
		p1 := <-waitingRoom
		p2 := <-waitingRoom

		globalMu.Lock()
		roomCount++
		roomID := roomCount
		globalMu.Unlock()

		room := &Room{
			id:       roomID,
			clients:  [2]*Client{p1, p2},
			troops:   []*Troop{},
			towerHP:  make(map[int]map[string]int),
			doneChan: make(chan struct{}),
		}
		room.towerHP[1] = map[string]int{"L": 100, "C": 100, "R": 100}
		room.towerHP[2] = map[string]int{"L": 100, "C": 100, "R": 100}
		p1.roomID = roomID
		p2.roomID = roomID
		rooms[roomID] = room

		go gameLoop(room)
	}
}

func gameLoop(room *Room) {
	defer func() {
		close(room.doneChan)
		globalMu.Lock()
		delete(rooms, room.id)
		globalMu.Unlock()
	}()

	p1 := room.clients[0]
	p2 := room.clients[1]

	startMsg := `Game started! Commands:
	1-L: Deploy type 1 to Left lane
	1-R: Deploy type 1 to Right lane
	2-L: Deploy type 2 to Left lane
	2-R: Deploy type 2 to Right lane
	Type exactly as shown (e.g. "2-R")`

	if p1.conn != nil {
		p1.conn.Write([]byte(fmt.Sprintf("%s\n", startMsg)))
	}
	if p2.conn != nil && p2 != p1 {
		p2.conn.Write([]byte(fmt.Sprintf("%s\n", startMsg)))
	}

	ticker := time.NewTicker(1* time.Second)
	defer ticker.Stop()

	gameOver := false
	winner := 0
	reason := ""

loop:
	for {
		select {
		case cmd, ok := <-p1.inputCh:
			if !ok {
				winner = 2
				reason = "disconnect"
				gameOver = true
				break loop
			}
			room.mu.Lock()
			processCommand(room, 1, cmd)
			room.mu.Unlock()

		case cmd, ok := <-p2.inputCh:
			if !ok {
				winner = 1
				reason = "disconnect"
				gameOver = true
				break loop
			}
			room.mu.Lock()
			processCommand(room, 2, cmd)
			room.mu.Unlock()

		case <-ticker.C:
			room.mu.Lock()
			if p1.mana < 100 {
				p1.mana += 1
			}
			if p2.mana < 100 {
				p2.mana += 1
			}

			updateTroops(room)
			applyTowerDamage(room)

			if room.towerHP[1]["C"] <= 0 {
				gameOver = true
				winner = 2
				reason = "king_tower"
				room.mu.Unlock()
				break loop
			}
			if room.towerHP[2]["C"] <= 0 {
				gameOver = true
				winner = 1
				reason = "king_tower"
				room.mu.Unlock()
				break loop
			}

			mapStr := renderMap(room)
			if p1.conn != nil {
				p1.conn.Write([]byte(fmt.Sprintf("%s_Mana: %d\n%s\n", p1.clientKey, p1.mana, mapStr)))
			}
			if p2.conn != nil && p2 != p1 {
				p2.conn.Write([]byte(fmt.Sprintf("%s_Mana: %d\n%s\n", p2.clientKey, p2.mana, mapStr)))
			}
			room.mu.Unlock()
		}
	}

	if gameOver {
		switch reason {
		case "king_tower":
			for _, c := range room.clients {
				if c.conn != nil {
					c.conn.Write([]byte(fmt.Sprintf("\nGAME OVER! Player %d wins by destroying the King Tower!\n", winner)))
				}
			}
		case "disconnect":
			if winner == 1 && p1.conn != nil {
				p1.conn.Write([]byte("\nGAME OVER! You win! Opponent disconnected.\n"))
			} else if winner == 2 && p2.conn != nil {
				p2.conn.Write([]byte("\nGAME OVER! You win! Opponent disconnected.\n"))
			}
		}

		// FIXED REPLAY SYSTEM
		// Ask both players if they want to replay
		for _, client := range room.clients {
			if client.clientKey == "Bot" {
				continue
			}
			if client.conn != nil {
				client.conn.Write([]byte("\nPlay again? (Y/N)\n"))
				client.ready = false
			}
		}

		// Wait for responses
		timeout := time.After(30 * time.Second)
		responsesReceived := 0
		totalPlayers := 0
		for _, client := range room.clients {
			if client.clientKey != "Bot" && client.conn != nil {
				totalPlayers++
			}
		}

		for responsesReceived < totalPlayers {
			select {
			case cmd, ok := <-p1.inputCh:
				if !ok {
					break
				}
				if handleReplayResponse(room, p1, cmd) {
					responsesReceived++
				}

			case cmd, ok := <-p2.inputCh:
				if !ok {
					break
				}
				if handleReplayResponse(room, p2, cmd) {
					responsesReceived++
				}

			case <-timeout:
				for _, c := range room.clients {
					if c.conn != nil {
						c.conn.Write([]byte("Replay timeout. Disconnecting.\n"))
						c.conn.Close()
					}
				}
				return
			}
		}

		// Check if all players want to replay
		allWantReplay := true
		for _, client := range room.clients {
			if client.clientKey != "Bot" && client.conn != nil && !client.ready {
				allWantReplay = false
			}
		}

		if allWantReplay {
			resetRoom(room)
			gameOver = false
			winner = 0
			reason = ""

			// Clear input channels
			for _, client := range room.clients {
				for len(client.inputCh) > 0 {
					<-client.inputCh
				}
			}

			// Send restart message
			startMsg := "Starting new game!\n"
			for _, client := range room.clients {
				if client.conn != nil {
					client.conn.Write([]byte(startMsg))
				}
			}

			// Restart game loop
			goto loop
		} else {
			for _, c := range room.clients {
				if c.conn != nil {
					c.conn.Write([]byte("Thanks for playing! Goodbye!\n"))
					c.conn.Close()
				}
			}
			return
		}
	}
}

// Helper function to handle replay responses
func handleReplayResponse(room *Room, client *Client, cmd string) bool {
	cmd = strings.ToUpper(strings.TrimSpace(cmd))
	if cmd == "Y" {
		client.ready = true
		client.conn.Write([]byte("Ready for next game!\n"))
		return true
	} else if cmd == "N" {
		client.ready = false
		client.conn.Write([]byte("Ending session. Goodbye!\n"))
		return true
	}
	return false
}

func processCommand(room *Room, player int, cmd string) {
	cmd = strings.ToUpper(strings.TrimSpace(cmd))
	var troopType int
	var lane string

	parts := strings.Split(cmd, "-")
	if len(parts) != 2 {
		return
	}

	troopTypeStr := parts[0]
	lane = parts[1]

	if troopTypeStr == "1" {
		troopType = 1
	} else if troopTypeStr == "2" {
		troopType = 2
	} else {
		return
	}

	if lane != "L" && lane != "R" {
		if room.clients[player-1].conn != nil {
			room.clients[player-1].conn.Write([]byte("Invalid lane! Use L or R.\n"))
		}
		return
	}

	var c *Client
	if player == 1 {
		c = room.clients[0]
	} else {
		c = room.clients[1]
	}

	if c.mana < 5 {
		if c.conn != nil {
			c.conn.Write([]byte("Not enough mana (need 5)!\n"))
		}
		return
	}
	c.mana -= 5

	newTroop := &Troop{
		player:    player,
		troopType: troopType,
		lane:      lane,
		position:  4,
		age:       0,
		alive:     true,
	}
	room.troops = append(room.troops, newTroop)

	if c.conn != nil {
		c.conn.Write([]byte(fmt.Sprintf("Deployed troop type %d to %s lane\n", troopType, lane)))
	}
}

func findEnemyTroopAt(room *Room, troop *Troop, pos int) *Troop {
	for _, t := range room.troops {
		if t.alive && t.player != troop.player && t.lane == troop.lane && t.position == pos {
			return t
		}
	}
	return nil
}

func updateTroops(room *Room) {
	var newTroops []*Troop

	for _, t := range room.troops {
		if !t.alive {
			continue
		}
		t.age++
		if t.age%2 != 0 {
			newTroops = append(newTroops, t)
			continue
		}

		nextPos := t.position - 1
		if nextPos < 0 {
			newTroops = append(newTroops, t)
			continue
		}

		enemy := findEnemyTroopAt(room, t, 4-t.position)
		if enemy != nil {
			if enemy.troopType == t.troopType {
				t.alive = false
				enemy.alive = false
			} else if enemy.troopType > t.troopType {
				t.alive = false
			} else {
				enemy.alive = false
				t.position = nextPos
			}
			newTroops = append(newTroops, t)
			continue
		}

		t.position = nextPos
		newTroops = append(newTroops, t)
	}

	var aliveTroops []*Troop
	for _, t := range newTroops {
		if t.alive {
			aliveTroops = append(aliveTroops, t)
		}
	}
	room.troops = aliveTroops
}

func applyTowerDamage(room *Room) {
	for _, t := range room.troops {
		if !t.alive || t.position != 0 {
			continue
		}

		enemyPlayer := 3 - t.player
		damage := 10
		if t.troopType == 2 {
			damage = 15
		}

		room.towerHP[enemyPlayer][t.lane] -= damage
		if room.towerHP[enemyPlayer][t.lane] < 0 {
			room.towerHP[enemyPlayer][t.lane] = 0
		}

		if (t.lane == "L" || t.lane == "R") && room.towerHP[enemyPlayer][t.lane] <= 0 {
			t.lane = "C"
			t.position = 4
		}
	}
}

func renderMap(room *Room) string {
	lanes := map[string][]string{
		"L": {" ", " ", " ", " ", " "},
		"C": {" ", " ", " ", " ", " "},
		"R": {" ", " ", " ", " ", " "},
	}

	for _, t := range room.troops {
		if !t.alive || t.position < 0 || t.position > 4 {
			continue
		}

		var typeChar string
		if t.troopType == 1 {
			typeChar = "A"
		} else {
			typeChar = "B"
		}

		symbol := fmt.Sprintf("%s%d", typeChar, t.player)

		if t.player == 2 {
			lanes[t.lane][t.position] = symbol
		} else {
			lanes[t.lane][4-t.position] = symbol
		}
	}

	formatHP := func(hp int) string {
		if hp < 5 {
			return "X"
		}
		return fmt.Sprintf("%d", hp)
	}

	p1HP := room.towerHP[1]
	p2HP := room.towerHP[2]

	lineSep := "                          --- --- --- --- ---"

	mapStr := fmt.Sprintf(`+---------------------- TEXT CLASH ROYALE MAP ----------------------+

[P1 TowerL - %s]  ===>  | %s | %s | %s | %s | %s |  <===  [P2 TowerL - %s]
%s
[P1 KingTower - %s] => | %s | %s | %s | %s | %s | <= [P2 KingTower - %s]
%s
[P1 TowerR - %s]  ===>  | %s | %s | %s | %s | %s |  <===  [P2 TowerR - %s]

+------------------------------------------------------------------+`,
		formatHP(p1HP["L"]), lanes["L"][0], lanes["L"][1], lanes["L"][2], lanes["L"][3], lanes["L"][4], formatHP(p2HP["L"]),
		lineSep,
		formatHP(p1HP["C"]), lanes["C"][0], lanes["C"][1], lanes["C"][2], lanes["C"][3], lanes["C"][4], formatHP(p2HP["C"]),
		lineSep,
		formatHP(p1HP["R"]), lanes["R"][0], lanes["R"][1], lanes["R"][2], lanes["R"][3], lanes["R"][4], formatHP(p2HP["R"]),
	)

	return mapStr
}
