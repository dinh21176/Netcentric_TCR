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
			// Player 1: đi từ trái -> phải
			lanes[t.lane][t.position] = symbol
		} else {
			// Player 2: đi từ phải -> trái (vị trí hiển thị ngược lại)
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

	// Kết cấu bản đồ đầy đủ
	mapStr := fmt.Sprintf(`
+---------------------- TEXT CLASH ROYALE MAP ----------------------+

[P1 TowerL - %s]  ===>  | %s | %s | %s | %s | %s |  <===  [P2 TowerL - %s]
%s
[P1 KingTower - %s] => | %s | %s | %s | %s | %s | <= [P2 KingTower - %s]
%s
[P1 TowerR - %s]  ===>  | %s | %s | %s | %s | %s |  <===  [P2 TowerR - %s]

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
