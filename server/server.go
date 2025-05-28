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
}

type Troop struct {
	player    int    // 1 or 2
	troopType int    // 1 or 2
	lane      string // "L", "C", "R"
	position  int    // 0..4 position on lane (0 nearest tower, 4 furthest)
	age       int    // for timing movement (move every 4 seconds)
	alive     bool
}

type Room struct {
	id      int
	clients [2]*Client
	troops  []*Troop
	towerHP map[int]map[string]int // player -> lane -> hp
	mu      sync.Mutex
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

func handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Read auth
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
		go startBotGame(client, level)

	case "2":
		conn.Write([]byte("Waiting for another player...\n"))
		waitingRoom <- client

	default:
		conn.Write([]byte("Invalid mode. Disconnecting.\n"))
		return
	}

	go listenClientInput(client)

	// Keep connection alive until client disconnects (detect error in listenClientInput)
	// Here, block until input channel closed or connection closed
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
	}

	room := &Room{
		id:      roomID,
		clients: [2]*Client{p1, bot},
		troops:  []*Troop{},
		towerHP: map[int]map[string]int{
			1: {"L": 100, "C": 100, "R": 100},
			2: {"L": 100, "C": 100, "R": 100},
		},
	}
	p1.roomID = roomID
	bot.roomID = roomID
	rooms[roomID] = room

	go func() {
		for {
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
			id:      roomID,
			clients: [2]*Client{p1, p2},
			troops:  []*Troop{},
			towerHP: map[int]map[string]int{
				1: {"L": 100, "C": 100, "R": 100},
				2: {"L": 100, "C": 100, "R": 100},
			},
		}
		p1.roomID = roomID
		p2.roomID = roomID
		rooms[roomID] = room

		go gameLoop(room)
	}
}

func gameLoop(room *Room) {
	p1 := room.clients[0]
	p2 := room.clients[1]

	startMsg := "Game started! Type number command to summon troops.\nCommands:\n1: 1-L\n2: 1-R\n3: 2-L\n4: 2-R\n"
	if p1.conn != nil {
		p1.conn.Write([]byte(fmt.Sprintf("%s\n", startMsg)))
	}
	if p2.conn != nil && p2 != p1 {
		p2.conn.Write([]byte(fmt.Sprintf("%s\n", startMsg)))
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case cmd, ok := <-p1.inputCh:
			if !ok {
				// client disconnected
				if p2.conn != nil {
					p2.conn.Write([]byte("Opponent disconnected. You win!\n"))
				}
				return
			}
			processCommand(room, 1, cmd)

		case cmd, ok := <-p2.inputCh:
			if !ok {
				if p1.conn != nil {
					p1.conn.Write([]byte("Opponent disconnected. You win!\n"))
				}
				return
			}
			processCommand(room, 2, cmd)

		case <-ticker.C:
			room.mu.Lock()
			// increase mana +1 per second
			if p1.mana < 100 {
				p1.mana += 1
			}
			if p2.mana < 100 {
				p2.mana += 1
			}

			updateTroops(room)
			applyTowerDamage(room)
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
}

func processCommand(room *Room, player int, cmd string) {
	room.mu.Lock()
	defer room.mu.Unlock()

	cmd = strings.ToUpper(cmd)
	var troopType int
	var lane string

	switch cmd {
	case "1-L", "1-R":
		troopType = 1
	case "2-L", "2-R":
		troopType = 2
	default:
		return
	}
	parts := strings.Split(cmd, "-")
	if len(parts) != 2 {
		return
	}
	lane = parts[1]

	// cost 5 mana
	var c *Client
	if player == 1 {
		c = room.clients[0]
	} else {
		c = room.clients[1]
	}
	if c.mana < 5 {
		c.conn.Write([]byte("Not enough mana!\n"))
		return
	}
	c.mana -= 5

	// add troop at position 4 (furthest)
	newTroop := &Troop{
		player:    player,
		troopType: troopType,
		lane:      lane,
		position:  4,
		age:       0,
		alive:     true,
	}
	room.troops = append(room.troops, newTroop)
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
		if t.age%4 != 0 {
			newTroops = append(newTroops, t)
			continue
		}

		nextPos := t.position - 1
		if nextPos < 0 {
			// Check tower HP của lane hiện tại
			hp := room.towerHP[3-t.player][t.lane]
			if hp <= 0 && t.lane != "C" {
				// Tower đã chết → kill troop gốc và spawn troop mới trên lane C
				t.alive = false
				newTroop := &Troop{
					player:    t.player,
					troopType: t.troopType,
					lane:      "C",
					position:  4,
					age:       0,
					alive:     true,
				}
				newTroops = append(newTroops, newTroop)
				continue
			} else {
				// Đang đánh tower chưa chết
				newTroops = append(newTroops, t)
				continue
			}
		}

		// Tìm enemy troop tại vị trí kế tiếp
		// enemy0 := findEnemyTroopAt(room, t, 4)  
		// if enemy0 != nil {
		// 	if enemy0.troopType == t.troopType {
		// 		t.alive = false
		// 		enemy0.alive = false
		// 	} else if enemy0.troopType > t.troopType {
		// 		t.alive = false
		// 	} else {
		// 		enemy0.alive = false
		// 	}
		// 	newTroops = append(newTroops, t) // giữ lại để sau lọc alive
		// 	continue
		// }
		// Tìm enemy troop tại vị trí kế tiếp
		enemy := findEnemyTroopAt(room, t, 4-t.position)  
		if enemy != nil {
			if enemy.troopType == t.troopType {
				t.alive = false
				enemy.alive = false
			} else if enemy.troopType > t.troopType {
				t.alive = false
			} else {
				enemy.alive = false
			}
			newTroops = append(newTroops, t) // giữ lại để sau lọc alive
			continue
		}
		

		// Không có enemy, tiến lên
		t.position = nextPos
		newTroops = append(newTroops, t)
	}

	// Lọc troop còn sống
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
		if !t.alive {
			continue
		}
		if t.position == 0 {
			enemyPlayer := 3 - t.player
			room.towerHP[enemyPlayer][t.lane] -= 5
			if room.towerHP[enemyPlayer][t.lane] <= 0 && t.lane != "C" {
				room.towerHP[enemyPlayer][t.lane] = 0
				// Game over
				msg := fmt.Sprintf("Tower %s of Player %d destroyed. Troop of %d is going to king lane \n", t.lane, enemyPlayer, t.player,)
				for _, c := range room.clients {
					if c.conn != nil {
						c.conn.Write([]byte(msg))
					}
				}
			}
			if room.towerHP[enemyPlayer][t.lane] <= 0 && t.lane == "C" {
				room.towerHP[enemyPlayer][t.lane] = 0
				// Game over
				msg := fmt.Sprintf("King Tower of Player %d destroyed by troop of %d. \n", enemyPlayer, t.player)
				for _, c := range room.clients {
					if c.conn != nil {
						c.conn.Write([]byte(msg))
					}
				}
			}
		}
	}
}

func renderMap(room *Room) string {
	// Tạo lane trống với 5 bước mỗi lane
	lanes := map[string][]string{
		"L": {" ", " ", " ", " ", " "},
		"C": {" ", " ", " ", " ", " "},
		"R": {" ", " ", " ", " ", " "},
	}

	// Đặt troop lên lane
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

	// Xử lý HP hiển thị
	formatHP := func(hp int) string {
		if hp <= 0 {
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



