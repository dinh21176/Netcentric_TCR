package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client represents a connected player or bot
type Client struct {
	conn      net.Conn
	username  string
	clientKey string
	roomID    int
	mana      int
	inputCh   chan string
	botLevel  int // 0 for human, 1-3 for bot difficulty
	gameMode  string
	ready     bool // Used for replay readiness
	exp       int  // Player's experience points
	level     int  // Player's level
}

// Troop represents a unit deployed on the map
type Troop struct {
	player    int    // Player ID (1 or 2)
	troopType int    // Type of troop (e.g., 1 or 2)
	lane      string // "L", "C", or "R"
	position  int    // Current position on the lane (0-4)
	age       int    // How many ticks the troop has been alive
	alive     bool   // Is the troop still active?
}

// Room represents a game session between two clients (or client and bot)
type Room struct {
	id       int
	clients  [2]*Client // Player 1 and Player 2 (or Bot)
	troops   []*Troop   // All active troops in the room
	towerHP  map[int]map[string]int // Tower HP for each player and lane
	mu       sync.Mutex             // Mutex to protect room data
	doneChan chan struct{}          // Channel to signal game over
}

// PlayerData stores persistent player information for saving/loading
type PlayerData struct {
	Username  string
	Password  string // NOTE: In production, hash this!
	ClientKey string
	Level     int
	Exp       int
}

var (
	clients        = make(map[string]*Client)    // All active client connections
	rooms          = make(map[int]*Room)        // All active game rooms
	waitingRoom    = make(chan *Client, 100)    // Channel for clients waiting for a PvP match
	clientCount    = 0                          // Global counter for client keys
	roomCount      = 0                          // Global counter for room IDs
	globalMu       sync.Mutex                   // Mutex to protect global maps (clients, rooms)
	playerDataFile = "players.json"             // File to store player data
	onlineUsers    = make(map[string]bool)      // Map to track currently logged-in usernames
	onlineUsersMu  sync.Mutex                   // Mutex to protect onlineUsers map
)

func main() {
	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
	defer ln.Close()

	fmt.Println("Server listening on port 8080...")

	go matchPlayers() // Start the goroutine for matching players

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Accept error:", err)
			continue
		}
		go handleConnection(conn) // Handle each new connection in a goroutine
	}
}

// --- Player Data Management ---

// loadPlayerData loads all player data from the JSON file
func loadPlayerData() (map[string]PlayerData, error) {
	players := make(map[string]PlayerData)
	data, err := os.ReadFile(playerDataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return players, nil // Return empty map if file doesn't exist
		}
		return nil, err
	}

	err = json.Unmarshal(data, &players)
	if err != nil {
		return nil, err
	}
	return players, nil
}

// savePlayerData updates a specific player's data in the global map and writes all data back to the JSON file
func savePlayerData(player PlayerData) error {
	players, err := loadPlayerData() // Load all existing player data
	if err != nil {
		return err
	}

	players[player.Username] = player // Update (or add) the specific player's data
	data, err := json.MarshalIndent(players, "", "  ") // Marshal the entire map back to JSON
	if err != nil {
		return err
	}

	return os.WriteFile(playerDataFile, data, 0644) // Write the updated JSON to file
}

// --- Game Logic Helpers ---

// resetRoom resets the game state for a given room
func resetRoom(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()

	room.towerHP[1] = map[string]int{"L": 100, "C": 100, "R": 100}
	room.towerHP[2] = map[string]int{"L": 100, "C": 100, "R": 100}
	room.troops = []*Troop{}

	for _, client := range room.clients {
		if client != nil {
			client.mana = 0
			client.ready = false // Reset ready state for replay
		}
	}
}

// requiredExpForLevel calculates the EXP needed for the next level
func requiredExpForLevel(level int) int {
	if level == 1 {
		return 100 // Require exactly 100 EXP to reach level 2
	}
	base := 100.0
	// EXP required increases exponentially with level for levels > 1
	return int(base * math.Pow(1.1, float64(level-1)))
}

// addExp adds experience points to a client and handles level-ups
func (c *Client) addExp(exp int) {
	c.exp += exp
	required := requiredExpForLevel(c.level)

	// Check for level-up
	for c.exp >= required {
		c.level++
		c.exp -= required // Carry over excess EXP to the next level
		required = requiredExpForLevel(c.level)

		if c.conn != nil {
			c.conn.Write([]byte(fmt.Sprintf("\n\n=== LEVEL UP! You've reached LEVEL %d! ===\n\n", c.level)))
		}
	}

	// Save updated player data (EXP and Level) to file
	playerData := PlayerData{
		Username:  c.username,
		Password:  "", // Password not needed for update, loaded from file
		ClientKey: c.clientKey,
		Level:     c.level,
		Exp:       c.exp,
	}
	if err := savePlayerData(playerData); err != nil {
		fmt.Println("Error saving player data for", c.username, ":", err)
	}
}

// --- Connection and Authentication Handling ---

func handleConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)

	// Defer function to handle client disconnection and cleanup
	defer func() {
		conn.Close()
		fmt.Printf("Client disconnected: %s\n", conn.RemoteAddr().String())

		// Find the username of the disconnected client and remove them from onlineUsers
		var disconnectedUsername string
		globalMu.Lock()
		for _, c := range clients {
			if c.conn == conn { // Check if this client object still points to the current connection
				disconnectedUsername = c.username
				delete(clients, c.clientKey) // Remove client from global clients map
				break
			}
		}
		globalMu.Unlock()

		if disconnectedUsername != "" {
			onlineUsersMu.Lock()
			delete(onlineUsers, disconnectedUsername)
			onlineUsersMu.Unlock()
			fmt.Printf("User %s is now offline.\n", disconnectedUsername)
		}
	}()

	// Read authentication line (username:password)
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

	// Load existing player data
	players, err := loadPlayerData()
	if err != nil {
		conn.Write([]byte("Server error: cannot load player data\n"))
		return
	}

	// --- Check for Duplicate Login ---
	onlineUsersMu.Lock()
	if onlineUsers[username] {
		onlineUsersMu.Unlock()
		conn.Write([]byte("Account is already logged in. Disconnecting.\n"))
		return // Disconnect immediately
	}
	onlineUsersMu.Unlock()
	// --- End Duplicate Login Check ---

	globalMu.Lock()
	clientCount++
	clientKey := fmt.Sprintf("C%d", clientCount)

	// Check if player exists
	player, exists := players[username]
	if !exists {
		// New player: register
		player = PlayerData{
			Username:  username,
			Password:  password, // TODO: Hash password in production!
			ClientKey: clientKey,
			Level:     1,
			Exp:       0,
		}
	} else {
		// Existing player: verify password
		if player.Password != password { // TODO: Compare hashed password in production!
			conn.Write([]byte("Incorrect password\n"))
			globalMu.Unlock()
			return
		}
		// Update client key for existing player
		player.ClientKey = clientKey
	}

	// Save updated player data
	if err := savePlayerData(player); err != nil {
		conn.Write([]byte("Server error: cannot save player data\n"))
		globalMu.Unlock()
		return
	}

	// Create new Client object
	client := &Client{
		conn:      conn,
		username:  username,
		clientKey: clientKey,
		mana:      0,
		inputCh:   make(chan string, 10),
		botLevel:  0,
		gameMode:  "",
		ready:     false,
		exp:       player.Exp,
		level:     player.Level,
	}
	clients[clientKey] = client
	globalMu.Unlock()

	// --- Mark user as online ---
	onlineUsersMu.Lock()
	onlineUsers[username] = true
	onlineUsersMu.Unlock()
	fmt.Printf("User %s is now online.\n", username)
	// --- End mark online ---

	// Send authentication success message
	conn.Write([]byte(fmt.Sprintf("%s_Authenticated. Level: %d, EXP: %d/%d\nChoose mode:\n1. Play vs Bot\n2. Play vs Player\n",
		clientKey, client.level, client.exp, requiredExpForLevel(client.level))))

	// Read game mode selection
	modeLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("Read mode error:", err)
		return
	}
	mode := strings.TrimSpace(modeLine)

	switch mode {
	case "1": // Play vs Bot
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

	case "2": // Play vs Player
		conn.Write([]byte("Waiting for another player...\n"))
		client.gameMode = "pvp"
		waitingRoom <- client

	default:
		conn.Write([]byte("Invalid mode. Disconnecting.\n"))
		return
	}

	// Start goroutine to listen for client game input
	go listenClientInput(client)

	select {}
}

// listenClientInput reads commands from a client's connection
func listenClientInput(client *Client) {
	reader := bufio.NewReader(client.conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			close(client.inputCh)
			return
		}
		line = strings.TrimSpace(line)
		client.inputCh <- line
	}
}

// --- Player Matching ---

func matchPlayers() {
	for {
		p1 := <-waitingRoom
		p2 := <-waitingRoom

		if p1.conn == nil || p2.conn == nil {
			fmt.Println("One or both clients disconnected before matching. Retrying match.")
			if p1.conn != nil {
				waitingRoom <- p1
			} else if p2.conn != nil {
				waitingRoom <- p2
			}
			continue
		}

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

		fmt.Printf("Room %d created for %s (%s) vs %s (%s)\n", roomID, p1.username, p1.clientKey, p2.username, p2.clientKey)

		go gameLoop(room)
	}
}

// startBotGame initializes and starts a game with a bot
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
		exp:       0,
		level:     1,
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

	fmt.Printf("Room %d created for %s (%s) vs %s\n", roomID, p1.username, p1.clientKey, bot.username)

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
						lanes := []string{"L", "R"}
						types := []string{"1", "2"}
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

// --- Game Loop and Core Mechanics ---

func gameLoop(room *Room) {
	defer func() {
		close(room.doneChan)
		globalMu.Lock()
		delete(rooms, room.id)
		globalMu.Unlock()

		for _, c := range room.clients {
			if c != nil && c.clientKey != "Bot" {
				onlineUsersMu.Lock()
				delete(onlineUsers, c.username)
				onlineUsersMu.Unlock()
				fmt.Printf("User %s is now offline (game ended).\n", c.username)

				if c.conn != nil {
					c.conn.Close()
				}
			}
		}
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

	ticker := time.NewTicker(2 * time.Second)
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
				p1.conn.Write([]byte(fmt.Sprintf("%s_Mana: %d, Level: %d, EXP: %d/%d\n%s\n",
					p1.clientKey, p1.mana, p1.level, p1.exp, requiredExpForLevel(p1.level), mapStr)))
			}
			if p2.conn != nil && p2 != p1 {
				p2.conn.Write([]byte(fmt.Sprintf("%s_Mana: %d, Level: %d, EXP: %d/%d\n%s\n",
					p2.clientKey, p2.mana, p2.level, p2.exp, requiredExpForLevel(p2.level), mapStr)))
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

			if winner == 1 && p1.clientKey != "Bot" {
				p1.addExp(30)
				if p1.conn != nil {
					p1.conn.Write([]byte(fmt.Sprintf("You gained 30 EXP! Total EXP: %d/%d\n",
						p1.exp, requiredExpForLevel(p1.level))))
				}
			} else if winner == 2 && p2.clientKey != "Bot" {
				p2.addExp(30)
				if p2.conn != nil {
					p2.conn.Write([]byte(fmt.Sprintf("You gained 30 EXP! Total EXP: %d/%d\n",
						p2.exp, requiredExpForLevel(p2.level))))
				}
			}

		case "disconnect":
			if winner == 1 && p1.clientKey != "Bot" {
				p1.conn.Write([]byte("\nGAME OVER! You win! Opponent disconnected.\n"))
				p1.addExp(30)
				if p1.conn != nil {
					p1.conn.Write([]byte(fmt.Sprintf("You gained 30 EXP! Total EXP: %d/%d\n",
						p1.exp, requiredExpForLevel(p1.level))))
				}
			} else if winner == 2 && p2.clientKey != "Bot" {
				p2.conn.Write([]byte("\nGAME OVER! You win! Opponent disconnected.\n"))
				p2.addExp(30)
				if p2.conn != nil {
					p2.conn.Write([]byte(fmt.Sprintf("You gained 30 EXP! Total EXP: %d/%d\n",
						p2.exp, requiredExpForLevel(p2.level))))
				}
			}
		}

		for _, client := range room.clients {
			if client.clientKey == "Bot" {
				continue
			}
			if client.conn != nil {
				client.conn.Write([]byte("\nPlay again? (Y/N)\n"))
				client.ready = false
			}
		}

		totalHumanPlayers := 0
		for _, client := range room.clients {
			if client.clientKey != "Bot" && client.conn != nil {
				totalHumanPlayers++
			}
		}

		replayResponseChan := make(chan bool, totalHumanPlayers)

		for _, client := range room.clients {
			if client.clientKey != "Bot" && client.conn != nil {
				go func(c *Client) {
					originalInputCh := c.inputCh
					tempReplayInputCh := make(chan string, 1)
					c.inputCh = tempReplayInputCh

					select {
					case cmd, ok := <-tempReplayInputCh:
						if ok && handleReplayResponse(room, c, cmd) {
							replayResponseChan <- true
						} else {
							replayResponseChan <- false
						}
					case <-time.After(20 * time.Second):
						if c.conn != nil {
							c.conn.Write([]byte("Replay response timeout.\n"))
						}
						replayResponseChan <- false
					}
					c.inputCh = originalInputCh
				}(client)
			}
		}

		responsesCollected := 0
		replayTimeout := time.After(30 * time.Second)
		for responsesCollected < totalHumanPlayers {
			select {
			case response := <-replayResponseChan:
				if response {
					responsesCollected++
				} else {
					responsesCollected++
				}
			case <-replayTimeout:
				fmt.Println("Replay negotiation timeout. Closing all client connections for room", room.id)
				return
			}
		}

		allWantReplay := true
		for _, client := range room.clients {
			if client.clientKey != "Bot" && client.conn != nil && !client.ready {
				allWantReplay = false
				break
			}
		}

		if allWantReplay {
			fmt.Printf("All players in Room %d ready for replay. Starting new game...\n", room.id)
			resetRoom(room)
			gameOver = false
			winner = 0
			reason = ""

			for _, client := range room.clients {
				for len(client.inputCh) > 0 {
					<-client.inputCh
				}
			}

			startMsg = "Starting new game!\n"
			for _, client := range room.clients {
				if client.conn != nil {
					client.conn.Write([]byte(startMsg))
				}
			}
			goto loop
		} else {
			fmt.Printf("Not all players in Room %d want to replay. Ending session.\n", room.id)
			for _, c := range room.clients {
				if c.conn != nil {
					c.conn.Write([]byte("Thanks for playing! Goodbye!\n"))
				}
			}
		}
	}
}

// handleReplayResponse processes Y/N input for replay prompt
func handleReplayResponse(room *Room, client *Client, cmd string) bool {
	cmd = strings.ToUpper(strings.TrimSpace(cmd))
	if cmd == "Y" {
		client.ready = true
		if client.conn != nil {
			client.conn.Write([]byte("Ready for next game!\n"))
		}
		return true
	} else if cmd == "N" {
		client.ready = false
		if client.conn != nil {
			client.conn.Write([]byte("Ending session. Goodbye!\n"))
			client.conn = nil
		}
		return true
	}
	if client.conn != nil {
		client.conn.Write([]byte("Invalid response. Please type Y or N.\n"))
	}
	return false
}

// --- Game Action Processors ---

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

// --- Map Rendering ---

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
