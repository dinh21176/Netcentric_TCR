// package main

// import (
// 	"bufio"
// 	"encoding/json"
// 	"fmt"
// 	"math/rand"
// 	"net"
// 	"os"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"time"
// )

// const (
// 	ServerPort       = ":8080"
// 	MaxMana          = 10
// 	ManaRegenRate    = 1
// 	GameDurationSecs = 180
// )

// type Tower struct {
// 	Name     string `json:"name"`
// 	HP       int    `json:"hp"`
// 	ATK      int    `json:"atk"`
// 	Defense  int    `json:"defense"`
// 	CritRate int    `json:"crit_rate"` // 0-100 percentage
// 	IsActive bool   `json:"is_active"`
// }

// type Troop struct {
// 	Name     string `json:"name"`
// 	HP       int    `json:"hp"`
// 	ATK      int    `json:"atk"`
// 	Defense  int    `json:"defense"`
// 	ManaCost int    `json:"mana_cost"`
// 	Owner    string `json:"owner"`
// 	IsActive bool   `json:"is_active"`
// }

// type PlayerData struct {
// 	Username string `json:"username"`
// 	Password string `json:"password"`
// 	Exp      int    `json:"exp"`
// 	Level    int    `json:"level"`
// }

// type Player struct {
// 	PlayerData
// 	GuardTower1 Tower   `json:"guard_tower_1"`
// 	GuardTower2 Tower   `json:"guard_tower_2"`
// 	KingTower   Tower   `json:"king_tower"`
// 	Troops      []Troop `json:"troops"`
// 	Conn        net.Conn
// 	Mana        int       `json:"mana"`
// 	LastManaReg time.Time `json:"last_mana_reg"`
// }

// type Game struct {
// 	Player1   *Player
// 	Player2   *Player
// 	Turn      int       `json:"turn"`
// 	StartTime time.Time `json:"start_time"`
// 	Mutex     sync.Mutex
// }

// var troopPool = []Troop{
// 	{"Pawn", 50, 150, 100, 3, "", true},
// 	{"Bishop", 100, 200, 150, 4, "", true},
// 	{"Rook", 250, 200, 200, 5, "", true},
// 	{"Knight", 200, 300, 150, 5, "", true},
// 	{"Prince", 500, 400, 300, 6, "", true},
// }

// func (t *Tower) IsDestroyed() bool {
// 	return t.HP <= 0
// }

// func (t *Troop) IsDestroyed() bool {
// 	return t.HP <= 0
// }

// func loadPlayerData() ([]PlayerData, error) {
// 	file, err := os.ReadFile("players.json")
// 	if os.IsNotExist(err) {
// 		// Create default players if file doesn't exist
// 		defaultPlayers := []PlayerData{
// 			{"player1", "pass1", 0, 1},
// 			{"player2", "pass2", 0, 1},
// 		}
// 		data, _ := json.MarshalIndent(defaultPlayers, "", "  ")
// 		os.WriteFile("players.json", data, 0644)
// 		return defaultPlayers, nil
// 	} else if err != nil {
// 		return nil, err
// 	}

// 	var players []PlayerData
// 	err = json.Unmarshal(file, &players)
// 	return players, err
// }

// func savePlayerData(players []PlayerData) error {
// 	data, err := json.MarshalIndent(players, "", "  ")
// 	if err != nil {
// 		return err
// 	}
// 	return os.WriteFile("players.json", data, 0644)
// }

// func NewPlayer(username, password string, level int) Player {
// 	// Apply level bonuses (10% per level)
// 	levelBonus := 1.0 + float64(level-1)*0.1

// 	// Select 3 random troops
// 	selected := make([]Troop, 3)
// 	perm := rand.Perm(len(troopPool))
// 	for i := 0; i < 3 && i < len(troopPool); i++ {
// 		troop := troopPool[perm[i]]
// 		troop.Owner = username
// 		troop.HP = int(float64(troop.HP) * levelBonus)
// 		troop.ATK = int(float64(troop.ATK) * levelBonus)
// 		troop.Defense = int(float64(troop.Defense) * levelBonus)
// 		selected[i] = troop
// 	}

// 	return Player{
// 		PlayerData: PlayerData{
// 			Username: username,
// 			Password: password,
// 			Level:    level,
// 		},
// 		GuardTower1: Tower{
// 			Name:     "Guard Tower 1",
// 			HP:       int(1000 * levelBonus),
// 			ATK:      int(300 * levelBonus),
// 			Defense:  int(100 * levelBonus),
// 			CritRate: 5,
// 			IsActive: true,
// 		},
// 		GuardTower2: Tower{
// 			Name:     "Guard Tower 2",
// 			HP:       int(1000 * levelBonus),
// 			ATK:      int(300 * levelBonus),
// 			Defense:  int(100 * levelBonus),
// 			CritRate: 5,
// 			IsActive: false,
// 		},
// 		KingTower: Tower{
// 			Name:     "King Tower",
// 			HP:       int(2000 * levelBonus),
// 			ATK:      int(500 * levelBonus),
// 			Defense:  int(300 * levelBonus),
// 			CritRate: 10,
// 			IsActive: false,
// 		},
// 		Troops:      selected,
// 		Mana:        5,
// 		LastManaReg: time.Now(),
// 	}
// }

// func (p *Player) NextTarget() *Tower {
// 	if p.GuardTower1.IsActive && !p.GuardTower1.IsDestroyed() {
// 		return &p.GuardTower1
// 	} else if !p.GuardTower2.IsDestroyed() {
// 		if p.GuardTower1.IsDestroyed() && !p.GuardTower2.IsActive {
// 			p.GuardTower2.IsActive = true
// 		}
// 		return &p.GuardTower2
// 	}
// 	if p.GuardTower1.IsDestroyed() && p.GuardTower2.IsDestroyed() && !p.KingTower.IsActive {
// 		p.KingTower.IsActive = true
// 	}
// 	return &p.KingTower
// }

// func (p *Player) GetActiveTroops() []Troop {
// 	var active []Troop
// 	for _, troop := range p.Troops {
// 		if !troop.IsDestroyed() {
// 			active = append(active, troop)
// 		}
// 	}
// 	return active
// }

// func (p *Player) RegenerateMana() {
// 	now := time.Now()
// 	seconds := int(now.Sub(p.LastManaReg).Seconds())
// 	if seconds > 0 {
// 		p.Mana += seconds * ManaRegenRate
// 		if p.Mana > MaxMana {
// 			p.Mana = MaxMana
// 		}
// 		p.LastManaReg = now
// 	}
// }

// func (p *Player) SendMessage(message string) {
// 	if p.Conn != nil {
// 		p.Conn.Write([]byte(message + "\n"))
// 	}
// }

// func (p *Player) GetStatus() string {
// 	var sb strings.Builder
// 	sb.WriteString(fmt.Sprintf("\n=== %s (Level %d) ===\n", p.Username, p.Level))
// 	sb.WriteString(fmt.Sprintf("Mana: %d/%d\n", p.Mana, MaxMana))

// 	sb.WriteString("\nTowers:\n")
// 	sb.WriteString(fmt.Sprintf("1. %s: HP %d/%d (ATK: %d, DEF: %d, CRIT: %d%%) %s\n",
// 		p.GuardTower1.Name, p.GuardTower1.HP, 1000*p.Level/1, p.GuardTower1.ATK, p.GuardTower1.Defense, p.GuardTower1.CritRate,
// 		map[bool]string{true: "[DESTROYED]", false: "[ACTIVE]"}[p.GuardTower1.IsDestroyed()]))

// 	sb.WriteString(fmt.Sprintf("2. %s: HP %d/%d (ATK: %d, DEF: %d, CRIT: %d%%) %s\n",
// 		p.GuardTower2.Name, p.GuardTower2.HP, 1000*p.Level/1, p.GuardTower2.ATK, p.GuardTower2.Defense, p.GuardTower2.CritRate,
// 		map[bool]string{
// 			true:  "[DESTROYED]",
// 			false: map[bool]string{true: "[ACTIVE]", false: "[INACTIVE]"}[p.GuardTower2.IsActive],
// 		}[p.GuardTower2.IsDestroyed()]))

// 	sb.WriteString(fmt.Sprintf("3. %s: HP %d/%d (ATK: %d, DEF: %d, CRIT: %d%%) %s\n",
// 		p.KingTower.Name, p.KingTower.HP, 2000*p.Level/1, p.KingTower.ATK, p.KingTower.Defense, p.KingTower.CritRate,
// 		map[bool]string{
// 			true:  "[DESTROYED]",
// 			false: map[bool]string{true: "[ACTIVE]", false: "[INACTIVE]"}[p.KingTower.IsActive],
// 		}[p.KingTower.IsDestroyed()]))

// 	sb.WriteString("\nTroops:\n")
// 	for i, troop := range p.Troops {
// 		sb.WriteString(fmt.Sprintf("%d. %s: HP %d/%d (ATK: %d, DEF: %d, Cost: %d) %s\n",
// 			i+1, troop.Name, troop.HP, troopPool[i].HP*p.Level/1, troop.ATK, troop.Defense, troop.ManaCost,
// 			map[bool]string{true: "[DESTROYED]", false: "[ACTIVE]"}[troop.IsDestroyed()]))
// 	}

// 	return sb.String()
// }

// func calculateDamage(attackerATK, attackerCritRate, defenderDEF int) int {
// 	// Check for critical hit
// 	isCrit := rand.Intn(100) < attackerCritRate
// 	damage := attackerATK - defenderDEF
// 	if isCrit {
// 		damage = int(float64(attackerATK)*1.2) - defenderDEF
// 	}
// 	if damage < 0 {
// 		damage = 0
// 	}
// 	return damage
// }

// func performAttack(attacker Troop, defender *Player) (string, bool) {
// 	var log strings.Builder
// 	gameOver := false

// 	for {
// 		target := defender.NextTarget()
// 		if !target.IsActive {
// 			break
// 		}

// 		damage := calculateDamage(attacker.ATK, 0, target.Defense) // Troops don't have crit
// 		target.HP -= damage
// 		if target.HP < 0 {
// 			target.HP = 0
// 		}

// 		log.WriteString(fmt.Sprintf("%s's %s attacks %s for %d damage! (HP left: %d)\n",
// 			attacker.Owner, attacker.Name, target.Name, damage, target.HP))

// 		if target.IsDestroyed() {
// 			log.WriteString(fmt.Sprintf(" %s has been destroyed!\n", target.Name))

// 			if target.Name == "King Tower" {
// 				log.WriteString(fmt.Sprintf(" %s WINS! The King Tower has fallen.\n", attacker.Owner))
// 				gameOver = true
// 				break
// 			}
// 		} else {
// 			break
// 		}
// 	}

// 	return log.String(), gameOver
// }

// func handlePlayerTurn(game *Game, currentPlayer *Player, opponent *Player) bool {
// 	currentPlayer.SendMessage("\n=== YOUR TURN ===")
// 	currentPlayer.SendMessage(currentPlayer.GetStatus())
// 	currentPlayer.SendMessage(opponent.GetStatus())

// 	// Regenerate mana
// 	currentPlayer.RegenerateMana()
// 	currentPlayer.SendMessage(fmt.Sprintf("Mana regenerated to %d/%d", currentPlayer.Mana, MaxMana))

// 	// Show available troops
// 	availableTroops := []Troop{}
// 	for _, troop := range currentPlayer.Troops {
// 		if !troop.IsDestroyed() && troop.ManaCost <= currentPlayer.Mana {
// 			availableTroops = append(availableTroops, troop)
// 		}
// 	}

// 	if len(availableTroops) == 0 {
// 		currentPlayer.SendMessage("You have no troops you can deploy (not enough mana or all destroyed)!")
// 		return false
// 	}

// 	currentPlayer.SendMessage("\nSelect a troop to deploy:")
// 	for i, troop := range availableTroops {
// 		currentPlayer.SendMessage(fmt.Sprintf("%d. %s (HP: %d, ATK: %d, DEF: %d, Cost: %d)",
// 			i+1, troop.Name, troop.HP, troop.ATK, troop.Defense, troop.ManaCost))
// 	}

// 	// Get player choice
// 	currentPlayer.SendMessage("Enter troop number: ")
// 	reader := bufio.NewReader(currentPlayer.Conn)
// 	choiceStr, err := reader.ReadString('\n')
// 	if err != nil {
// 		currentPlayer.SendMessage("Error reading your choice. Skipping turn.")
// 		return false
// 	}

// 	choiceStr = strings.TrimSpace(choiceStr)
// 	choice, err := strconv.Atoi(choiceStr)
// 	if err != nil || choice < 1 || choice > len(availableTroops) {
// 		currentPlayer.SendMessage("Invalid choice. Skipping turn.")
// 		return false
// 	}

// 	selectedTroop := availableTroops[choice-1]
// 	currentPlayer.Mana -= selectedTroop.ManaCost
// 	log, gameOver := performAttack(selectedTroop, opponent)
// 	currentPlayer.SendMessage(log)
// 	opponent.SendMessage(log)

// 	return gameOver
// }

// func gameLoop(game *Game) {
// 	game.StartTime = time.Now()
// 	currentPlayer := game.Player1
// 	opponent := game.Player2

// 	for {
// 		// Check game time
// 		if time.Since(game.StartTime).Seconds() >= GameDurationSecs {
// 			game.Player1.SendMessage("\nTime's up! Game ended in a draw.")
// 			game.Player2.SendMessage("\nTime's up! Game ended in a draw.")
// 			break
// 		}

// 		game.Mutex.Lock()
// 		gameOver := handlePlayerTurn(game, currentPlayer, opponent)
// 		game.Mutex.Unlock()

// 		if gameOver {
// 			break
// 		}

// 		// Switch players
// 		if currentPlayer == game.Player1 {
// 			currentPlayer = game.Player2
// 			opponent = game.Player1
// 		} else {
// 			currentPlayer = game.Player1
// 			opponent = game.Player2
// 		}

// 		time.Sleep(500 * time.Millisecond)
// 	}

// 	// Game over cleanup
// 	game.Player1.Conn.Close()
// 	game.Player2.Conn.Close()
// }

// func main() {
// 	rand.Seed(time.Now().UnixNano())

// 	// Local testing without network
// 	player1 := NewPlayer("player1", "pass1", 1)
// 	player2 := NewPlayer("player2", "pass2", 1)

// 	game := &Game{
// 		Player1: &player1,
// 		Player2: &player2,
// 	}

// 	localGameLoop(game)
// }

// func localHandlePlayerTurn(game *Game, currentPlayer *Player, opponent *Player, reader *bufio.Reader) bool {
// 	fmt.Printf("\n=== %s's TURN ===\n", currentPlayer.Username)
// 	fmt.Println(currentPlayer.GetStatus())
// 	fmt.Println(opponent.GetStatus())

// 	activeTroops := currentPlayer.GetActiveTroops()
// 	if len(activeTroops) == 0 {
// 		fmt.Println("You have no active troops to attack with!")
// 		return false
// 	}

// 	// Show available troops
// 	fmt.Println("\nSelect a troop to attack with:")
// 	for i, troop := range activeTroops {
// 		fmt.Printf("%d. %s (HP: %d, ATK: %d, DEF: %d)\n",
// 			i+1, troop.Name, troop.HP, troop.ATK, troop.Defense)
// 	}

// 	// Get player choice
// 	fmt.Print("Enter troop number: ")
// 	choiceStr, err := reader.ReadString('\n')
// 	if err != nil {
// 		fmt.Println("Error reading your choice. Skipping turn.")
// 		return false
// 	}

// 	choiceStr = strings.TrimSpace(choiceStr)
// 	choice, err := strconv.Atoi(choiceStr)
// 	if err != nil || choice < 1 || choice > len(activeTroops) {
// 		fmt.Println("Invalid choice. Skipping turn.")
// 		return false
// 	}

// 	selectedTroop := activeTroops[choice-1]
// 	log, gameOver := performAttack(selectedTroop, opponent)
// 	fmt.Println(log)

// 	return gameOver
// }

// func localGameLoop(game *Game) {
// 	reader := bufio.NewReader(os.Stdin)
// 	currentPlayer := game.Player1
// 	opponent := game.Player2

// 	for {
// 		gameOver := localHandlePlayerTurn(game, currentPlayer, opponent, reader)
// 		if gameOver {
// 			break
// 		}

// 		// Switch players
// 		if currentPlayer == game.Player1 {
// 			currentPlayer = game.Player2
// 			opponent = game.Player1
// 		} else {
// 			currentPlayer = game.Player1
// 			opponent = game.Player2
// 		}

// 		time.Sleep(500 * time.Millisecond)
// 	}

// 	fmt.Println("\nGame over! Thanks for playing.")
// }
