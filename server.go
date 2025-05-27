package main

import (
	"bufio"
	"fmt"
	"net"
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
	player     int    // 1 or 2
	troopType  int    // 1 or 2
	lane       string // "L", "C", "R"
	position   int    // 0..4 position on lane (0 nearest tower, 4 furthest)
	age        int    // for timing movement (move every 4 seconds)
	alive      bool
}

type Room struct {
	id          int
	clients     [2]*Client
	troops      []*Troop
	towerHP     map[int]map[string]int // player -> lane -> hp
	mu          sync.Mutex
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

	conn.Write([]byte(fmt.Sprintf("%s_Authenticated. Waiting for another player...\n", clientKey)))

	waitingRoom <- client

	go listenClientInput(client)

	// Keep connection alive
	select {}
}

func listenClientInput(client *Client) {
	reader := bufio.NewReader(client.conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Client disconnected:", client.clientKey)
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

	// Notify start game
	startMsg := "Game started! Type number command to summon troops.\nCommands:\n1: 1-L\n2: 1-R\n3: 2-L\n4: 2-R\n"
	p1.conn.Write([]byte(fmt.Sprintf("%s_%s", p1.clientKey, startMsg)))
	p2.conn.Write([]byte(fmt.Sprintf("%s_%s", p2.clientKey, startMsg)))

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// mana increase every second
	go func() {
		for range ticker.C {
			room.mu.Lock()
			p1.mana += 1
			p2.mana += 1
			room.mu.Unlock()
		}
	}()

	for {
		select {
		case cmd := <-p1.inputCh:
			handleCommand(room, 1, cmd)
		case cmd := <-p2.inputCh:
			handleCommand(room, 2, cmd)
		case <-ticker.C:
			room.mu.Lock()
			// update troop movement
			updateTroops(room)
			// apply troop damage to towers if at position 0
			applyTowerDamage(room)
			// render and send map
			mapStr := renderMap(room)
			// send map + mana to clients
			p1.conn.Write([]byte(fmt.Sprintf("%s_Mana: %d\n%s\n", p1.clientKey, p1.mana, mapStr)))
			p2.conn.Write([]byte(fmt.Sprintf("%s_Mana: %d\n%s\n", p2.clientKey, p2.mana, mapStr)))
			room.mu.Unlock()
		}
	}
}

func handleCommand(room *Room, player int, cmd string) {
	room.mu.Lock()
	defer room.mu.Unlock()

	var client *Client
	if player == 1 {
		client = room.clients[0]
	} else {
		client = room.clients[1]
	}

	// Commands: "1", "2", "3", "4"
	// map commands to troop summon: 1=1-L, 2=1-R, 3=2-L, 4=2-R
	var troopType int
	var lane string
	switch cmd {
	case "1":
		troopType = 1
		lane = "L"
	case "2":
		troopType = 1
		lane = "R"
	case "3":
		troopType = 2
		lane = "L"
	case "4":
		troopType = 2
		lane = "R"
	default:
		client.conn.Write([]byte("Invalid command\n"))
		return
	}

	// mana cost per troop 5 for type1, 8 for type2 (example)
	cost := 0
	if troopType == 1 {
		cost = 5
	} else {
		cost = 8
	}

	if client.mana < cost {
		client.conn.Write([]byte("Not enough mana\n"))
		return
	}

	client.mana -= cost

	// spawn troop at position 4 (furthest from enemy tower)
	troop := &Troop{
		player:    player,
		troopType: troopType,
		lane:      lane,
		position:  4,
		age:       0,
		alive:     true,
	}
	room.troops = append(room.troops, troop)
}

func updateTroops(room *Room) {
	for _, t := range room.troops {
		if !t.alive {
			continue
		}
		t.age++
		if t.age%4 == 0 { // move every 4 seconds
			// check next position
			nextPos := t.position - 1
			if nextPos < 0 {
				// reached tower position
				continue
			}

			// Check if tower at nextPos is destroyed (hp=0)
			if nextPos == 0 {
				hp := room.towerHP[3-t.player][t.lane]
				if hp == 0 {
					// Tower destroyed, troop jumps to lane center at pos=4 of enemy
					t.lane = "C"
					t.position = 4
					t.age = 0
					continue
				}
			}

			// Check if enemy troop occupies nextPos same lane
			enemyTroop := findEnemyTroopAt(room, t, nextPos)
			if enemyTroop != nil {
				// Battle logic
				// If same troopType -> both die
				if enemyTroop.troopType == t.troopType {
					t.alive = false
					enemyTroop.alive = false
				} else {
					// Different troopType: weaker die
					if enemyTroop.troopType > t.troopType {
						t.alive = false
					} else {
						enemyTroop.alive = false
					}
				}
				continue
			}

			// Move troop forward
			t.position = nextPos
		}
	}
	// Remove dead troops
	var aliveTroops []*Troop
	for _, t := range room.troops {
		if t.alive {
			aliveTroops = append(aliveTroops, t)
		}
	}
	room.troops = aliveTroops
}

func findEnemyTroopAt(room *Room, troop *Troop, pos int) *Troop {
	for _, t := range room.troops {
		if t.alive && t.player != troop.player && t.lane == troop.lane && t.position == pos {
			return t
		}
	}
	return nil
}

func applyTowerDamage(room *Room) {
	// troops at position 0 damage tower -5hp per second
	for _, t := range room.troops {
		if !t.alive {
			continue
		}
		if t.position == 0 {
			enemy := 3 - t.player
			hp := room.towerHP[enemy][t.lane]
			if hp > 0 {
				room.towerHP[enemy][t.lane] -= 5
				if room.towerHP[enemy][t.lane] < 0 {
					room.towerHP[enemy][t.lane] = 0
				}
			}
		}
	}
}

func renderMap(room *Room) string {
	// Prepare empty map lanes: 5 positions mỗi lane, mặc định "   "
	lanes := map[string][]string{
		"L": {"   ", "   ", "   ", "   ", "   "},
		"C": {"   ", "   ", "   ", "   ", "   "},
		"R": {"   ", "   ", "   ", "   ", "   "},
	}

	// Place troops with correct directions:
	for _, t := range room.troops {
		if !t.alive {
			continue
		}

		var sym string
		if t.player == 1 {
			// Player 1 troop, A or B type
			if t.troopType == 1 {
				sym = "A1 "
			} else {
				sym = "B1 "
			}
			// pos đúng vị trí troop
			lanes[t.lane][t.position] = sym
		} else if t.player == 2 {
			// Player 2 troop, A or B type
			if t.troopType == 1 {
				sym = "A2 "
			} else {
				sym = "B2 "
			}
			// Player 2 đi ngược, vị trí map ngược lại: pos 4 - t.position
			revPos := 4 - t.position
			lanes[t.lane][revPos] = sym
		}
	}

	// Format HP tower, <=0 hiển thị X
	formatHP := func(hp int) string {
		if hp <= 0 {
			return "X"
		}
		return fmt.Sprintf("%d", hp)
	}

	p1HP := room.towerHP[1]
	p2HP := room.towerHP[2]

	lineSep := "                      --- --- --- --- ---"

	// Build map string
	mapStr := fmt.Sprintf(`
+---------------------- TEXT CLASH ROYALE MAP ----------------------+

[P1 TowerL - %s]  ===>  |%s|%s|%s|%s|%s|  <===  [P2 TowerL - %s]
%s
[P1 KingTower - %s] => |%s|%s|%s|%s|%s| <= [P2 KingTower - %s]
%s
[P1 TowerR - %s]  ===>  |%s|%s|%s|%s|%s|  <===  [P2 TowerR - %s]

+------------------------------------------------------------------+
`,
		formatHP(p1HP["L"]), lanes["L"][0], lanes["L"][1], lanes["L"][2], lanes["L"][3], lanes["L"][4], formatHP(p2HP["L"]),
		lineSep,
		formatHP(p1HP["C"]), lanes["C"][0], lanes["C"][1], lanes["C"][2], lanes["C"][3], lanes["C"][4], formatHP(p2HP["C"]),
		lineSep,
		formatHP(p1HP["R"]), lanes["R"][0], lanes["R"][1], lanes["R"][2], lanes["R"][3], lanes["R"][4], formatHP(p2HP["R"]),
	)

	return mapStr
}
