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
	troopType string // Type of troop (P, B, R, K, I, Q)
	lane      string // "L", "C", or "R"
	position  int    // Current position on the lane (0-4)
	age       int    // How many ticks the troop has been alive
	alive     bool   // Is the troop still active?
	hp        int    // Current HP
	atk       int    // Attack power
	def       int    // Defense
}

// Tower represents a defensive structure
type Tower struct {
	hp   int
	atk  int
	def  int
	crit float64
}

// Room represents a game session between two clients (or client and bot)
type Room struct {
	id       int
	clients  [2]*Client                // Player 1 and Player 2 (or Bot)
	troops   []*Troop                  // All active troops in the room
	towerHP  map[int]map[string]int    // Tower HP for display
	tower    map[int]map[string]*Tower // Tower stats
	mu       sync.Mutex                // Mutex to protect room data
	doneChan chan struct{}             // Channel to signal game over
	started  time.Time                 // Game start time
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
	clients        = make(map[string]*Client) // All active client connections
	rooms          = make(map[int]*Room)      // All active game rooms
	waitingRoom    = make(chan *Client, 100)  // Channel for clients waiting for a PvP match
	clientCount    = 0                        // Global counter for client keys
	roomCount      = 0                        // Global counter for room IDs
	globalMu       sync.Mutex                 // Mutex to protect global maps (clients, rooms)
	playerDataFile = "players.json"           // File to store player data
	onlineUsers    = make(map[string]bool)    // Map to track currently logged-in usernames
	onlineUsersMu  sync.Mutex                 // Mutex to protect onlineUsers map

	// Troop types and their base stats
	troopTypes = map[string]struct {
		hp   int
		atk  int
		def  int
		mana int
		spec string
	}{
		"P": {50, 150, 100, 3, ""},  // Pawn
		"B": {100, 200, 150, 4, ""}, // Bishop
		"R": {250, 200, 200, 5, ""}, // Rook
		"K": {200, 300, 150, 5, ""}, // Knight
		"I": {500, 400, 300, 6, ""}, // Prince
		"Q": {1, 0, 0, 5, "heal"},   // Queen (special: heal tower)
	}

	// Tower base stats
	towerBaseStats = map[string]struct {
		hp   int
		atk  int
		def  int
		crit float64
	}{
		"King":  {2000, 500, 300, 10},
		"Guard": {1000, 300, 100, 5},
	}
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

	players[player.Username] = player                  // Update (or add) the specific player's data
	data, err := json.MarshalIndent(players, "", "  ") // Marshal the entire map back to JSON
	if err != nil {
		return err
	}

	return os.WriteFile(playerDataFile, data, 0644) // Write the updated JSON to file
}

// --- Game Logic Helpers ---

// Fixed applyLevelScaling function
func (c *Client) applyLevelScaling(base int) int {
	return int(float64(base) * (1.0 + 0.1*float64(c.level)))
}

// resetRoom resets the game state for a given room
func resetRoom(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()

	// Initialize towers
	room.tower = make(map[int]map[string]*Tower)
	room.towerHP = make(map[int]map[string]int)

	// Player 1 towers
	room.tower[1] = make(map[string]*Tower)
	room.towerHP[1] = make(map[string]int)

	// Player 2 towers
	room.tower[2] = make(map[string]*Tower)
	room.towerHP[2] = make(map[string]int)

	// Set tower stats with level scaling
	for playerID, client := range room.clients {
		if client == nil {
			continue
		}
		playerNum := playerID + 1

		// King Tower (Center)
		kingStats := towerBaseStats["King"]
		room.tower[playerNum]["C"] = &Tower{
			hp:   client.applyLevelScaling(kingStats.hp),
			atk:  client.applyLevelScaling(kingStats.atk),
			def:  client.applyLevelScaling(kingStats.def),
			crit: kingStats.crit,
		}
		room.towerHP[playerNum]["C"] = room.tower[playerNum]["C"].hp

		// Guard Towers (Left and Right)
		guardStats := towerBaseStats["Guard"]
		room.tower[playerNum]["L"] = &Tower{
			hp:   client.applyLevelScaling(guardStats.hp),
			atk:  client.applyLevelScaling(guardStats.atk),
			def:  client.applyLevelScaling(guardStats.def),
			crit: guardStats.crit,
		}
		room.towerHP[playerNum]["L"] = room.tower[playerNum]["L"].hp

		room.tower[playerNum]["R"] = &Tower{
			hp:   client.applyLevelScaling(guardStats.hp),
			atk:  client.applyLevelScaling(guardStats.atk),
			def:  client.applyLevelScaling(guardStats.def),
			crit: guardStats.crit,
		}
		room.towerHP[playerNum]["R"] = room.tower[playerNum]["R"].hp
	}

	room.troops = []*Troop{}
	room.started = time.Now()

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

// calculateDamage computes damage using CRIT chance and defense
func calculateDamage(atk int, critChance float64, def int) int {
	// Check for critical hit
	isCrit := rand.Float64()*100 < critChance
	baseDamage := atk
	if isCrit {
		baseDamage = int(float64(atk) * 1.2)
	}

	damage := baseDamage - def
	if damage < 0 {
		return 0
	}
	return damage
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
			tower:    make(map[int]map[string]*Tower),
			towerHP:  make(map[int]map[string]int),
			doneChan: make(chan struct{}),
		}
		p1.roomID = roomID
		p2.roomID = roomID
		rooms[roomID] = room
		resetRoom(room)

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
		tower:    make(map[int]map[string]*Tower),
		towerHP:  make(map[int]map[string]int),
		doneChan: make(chan struct{}),
	}
	p1.roomID = roomID
	bot.roomID = roomID
	rooms[roomID] = room
	resetRoom(room)

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
						command = "P-L"
					case 2:
						command = "B-C"
					case 3:
						troops := []string{"P", "B", "R", "K", "I"}
						lanes := []string{"L", "R"}
						command = fmt.Sprintf("%s-%s", troops[rand.Intn(len(troops))], lanes[rand.Intn(len(lanes))])
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
    P-L: Deploy Pawn to Left lane
    B-C: Deploy Bishop to Center lane
    R-R: Deploy Rook to Right lane
    K-L: Deploy Knight to Left lane
    I-R: Deploy Prince to Right lane
    Q-C: Deploy Queen to Center lane

    Strategy:
    - Destroy both Left and Right Towers before attacking the King Tower
    - Queen heals friendly towers when she reaches them`

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
			applyCombat(room)

			// Check king tower for game end
			if room.tower[1]["C"].hp <= 0 {
				gameOver = true
				winner = 2
				reason = "king_tower"
				room.mu.Unlock()
				break loop
			}
			if room.tower[2]["C"].hp <= 0 {
				gameOver = true
				winner = 1
				reason = "king_tower"
				room.mu.Unlock()
				break loop
			}

			// Check game timer (3 minutes)
			if time.Since(room.started) >= 3*time.Minute {
				gameOver = true
				reason = "time_up"
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
		// Handle time_up win condition
		if reason == "time_up" {
			p1TowersDestroyed := 0
			p2TowersDestroyed := 0

			// Count destroyed towers (both guard and king)
			for _, tower := range room.tower[1] {
				if tower.hp <= 0 {
					p2TowersDestroyed++ // Player 2 destroyed Player 1's tower
				}
			}
			for _, tower := range room.tower[2] {
				if tower.hp <= 0 {
					p1TowersDestroyed++ // Player 1 destroyed Player 2's tower
				}
			}

			if p1TowersDestroyed > p2TowersDestroyed {
				winner = 1
			} else if p2TowersDestroyed > p1TowersDestroyed {
				winner = 2
			} else {
				winner = 0 // Draw
			}
		}

		// Send game over message
		for _, c := range room.clients {
			if c.conn == nil {
				continue
			}

			switch {
			case winner == 0:
				c.conn.Write([]byte("\nGAME OVER! It's a draw!\n"))
			case c == room.clients[winner-1]:
				c.conn.Write([]byte(fmt.Sprintf("\nGAME OVER! You win! (%s)\n", reason)))
				c.addExp(30)
				c.conn.Write([]byte(fmt.Sprintf("You gained 30 EXP! Total EXP: %d/%d\n",
					c.exp, requiredExpForLevel(c.level))))
			default:
				c.conn.Write([]byte(fmt.Sprintf("\nGAME OVER! Player %d wins! (%s)\n", winner, reason)))
			}
		}

		// Handle replay
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
	var troopType string
	var lane string

	parts := strings.Split(cmd, "-")
	if len(parts) != 2 {
		return
	}

	troopType = parts[0]
	lane = parts[1]

	// Validate troop type
	if _, valid := troopTypes[troopType]; !valid {
		if room.clients[player-1].conn != nil {
			room.clients[player-1].conn.Write([]byte("Invalid troop type! Use P, B, R, K, I, or Q.\n"))
		}
		return
	}

	// Validate lane
	if lane != "L" && lane != "C" && lane != "R" {
		if room.clients[player-1].conn != nil {
			room.clients[player-1].conn.Write([]byte("Invalid lane! Use L, C, or R.\n"))
		}
		return
	}

	var c *Client
	if player == 1 {
		c = room.clients[0]
	} else {
		c = room.clients[1]
	}

	// Check mana cost
	requiredMana := troopTypes[troopType].mana
	if c.mana < requiredMana {
		if c.conn != nil {
			c.conn.Write([]byte(fmt.Sprintf("Not enough mana (need %d)!\n", requiredMana)))
		}
		return
	}
	c.mana -= requiredMana

	// Create troop with level-scaled stats
	base := troopTypes[troopType]
	newTroop := &Troop{
		player:    player,
		troopType: troopType,
		lane:      lane,
		position:  4,
		age:       0,
		alive:     true,
		hp:        c.applyLevelScaling(base.hp),
		atk:       c.applyLevelScaling(base.atk),
		def:       c.applyLevelScaling(base.def),
	}
	room.troops = append(room.troops, newTroop)

	if c.conn != nil {
		c.conn.Write([]byte(fmt.Sprintf("Deployed %s to %s lane\n", getTroopName(troopType), lane)))
	}
}

// getTroopName returns the full name of a troop
func getTroopName(troopType string) string {
	names := map[string]string{
		"P": "Pawn",
		"B": "Bishop",
		"R": "Rook",
		"K": "Knight",
		"I": "Prince",
		"Q": "Queen",
	}
	return names[troopType]
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

		// Calculate enemy position (mirrored)
		enemyPos := 4 - t.position
		enemy := findEnemyTroopAt(room, t, enemyPos)
		if enemy != nil {
			// Troop vs troop combat
			troopDamage := calculateDamage(t.atk, 0, enemy.def)
			enemy.hp -= troopDamage

			enemyDamage := calculateDamage(enemy.atk, 0, t.def)
			t.hp -= enemyDamage

			if t.hp <= 0 {
				t.alive = false
			}
			if enemy.hp <= 0 {
				enemy.alive = false
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

// applyCombat handles troop-tower interactions and special abilities
func applyCombat(room *Room) {
	for _, t := range room.troops {
		if !t.alive || t.position != 0 {
			continue
		}

		enemyPlayer := 3 - t.player
		tower := room.tower[enemyPlayer][t.lane]

		// Handle Queen special ability (heals friendly tower)
		if t.troopType == "Q" {
			// Find lowest HP friendly tower
			friendlyPlayer := t.player
			lowestHP := 1000000
			var healLane string

			for lane, tower := range room.tower[friendlyPlayer] {
				if tower.hp > 0 && tower.hp < lowestHP {
					lowestHP = tower.hp
					healLane = lane
				}
			}

			// Heal the tower
			if healLane != "" {
				tower := room.tower[friendlyPlayer][healLane]
				healAmount := 300
				tower.hp += healAmount
				room.towerHP[friendlyPlayer][healLane] = tower.hp

				// Notify players
				for _, c := range room.clients {
					if c.conn != nil {
						c.conn.Write([]byte(
							fmt.Sprintf("Queen healed Player %d's %s tower by %d HP!\n",
								friendlyPlayer, healLane, healAmount)))
					}
				}
			}

			// Remove Queen after healing
			t.alive = false
			continue
		}

		// Normal troop attacks tower
		troopDamage := calculateDamage(t.atk, 0, tower.def)
		tower.hp -= troopDamage
		room.towerHP[enemyPlayer][t.lane] = tower.hp

		// Tower attacks troop
		towerDamage := calculateDamage(tower.atk, tower.crit, t.def)
		t.hp -= towerDamage

		if t.hp <= 0 {
			t.alive = false
		}

		// Enhanced tower destruction logic
		if tower.hp <= 0 {
			// Tower destroyed - check if it's a guard tower
			if t.lane == "L" || t.lane == "R" {
				// Check if both guard towers are destroyed
				if room.tower[enemyPlayer]["L"].hp <= 0 && room.tower[enemyPlayer]["R"].hp <= 0 {
					// Both side towers destroyed - move to center
					t.lane = "C"
					t.position = 4
				} else if room.tower[enemyPlayer]["L"].hp <= 0 {
					// Only left tower destroyed - move to right tower
					t.lane = "R"
					t.position = 4
				} else if room.tower[enemyPlayer]["R"].hp <= 0 {
					// Only right tower destroyed - move to left tower
					t.lane = "L"
					t.position = 4
				}
			}
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

		symbol := fmt.Sprintf("%s%d", t.troopType, t.player)

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
