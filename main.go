package main

import (
	"fmt"
	"math/rand"
	"time"
)

type Tower struct {
	Name    string
	HP      int
	ATK     int
	Defense int
}

func (t *Tower) IsDestroyed() bool {
	return t.HP <= 0
}

type Troop struct {
	Name    string
	HP      int
	ATK     int
	Defense int
	Owner   string
}

type Player struct {
	Username    string
	GuardTower1 Tower
	GuardTower2 Tower
	KingTower   Tower
	Troops      []Troop
}

var troopPool = []Troop{
	{"Prince", 800, 400, 150, ""},
	{"Knight", 1000, 300, 200, ""},
	{"Pawn", 600, 150, 100, ""},
	{"Archer", 500, 200, 100, ""},
	{"Giant", 1500, 250, 300, ""},
}

func NewPlayer(username string) Player {
	selected := make([]Troop, 3)
	perm := rand.Perm(len(troopPool))
	for i := 0; i < 3; i++ {
		troop := troopPool[perm[i]]
		troop.Owner = username
		selected[i] = troop
	}
	return Player{
		Username:    username,
		GuardTower1: Tower{"Guard Tower 1", 1000, 200, 250},
		GuardTower2: Tower{"Guard Tower 2", 1200, 250, 300},
		KingTower:   Tower{"King Tower", 2000, 300, 300},
		Troops:      selected,
	}
}

func (p *Player) NextTarget() *Tower {
	if !p.GuardTower1.IsDestroyed() {
		return &p.GuardTower1
	} else if !p.GuardTower2.IsDestroyed() {
		return &p.GuardTower2
	}
	return &p.KingTower
}

// Modify the tower damage here
func performAttack(attacker Troop, defender *Player) (string, bool) {
	log := ""
	for {
		target := defender.NextTarget()
		damage := attacker.ATK - target.Defense
		if damage < 0 {
			damage = 0
		}
		target.HP -= damage
		if target.HP < 0 {
			target.HP = 0
		}
		log += fmt.Sprintf("%s's %s attacks %s for %d damage! (HP left: %d)\n",
			attacker.Owner, attacker.Name, target.Name, damage, target.HP)

		if target.IsDestroyed() {
			log += fmt.Sprintf(" %s has been destroyed!\n", target.Name)
			if target.Name == "King Tower" {
				log += fmt.Sprintf(" %s WINS! The King Tower has fallen.\n", attacker.Owner)
				return log, true
			}
		} else {
			break // Stop attacking if target survived
		}
	}
	return log, false
}

func simulateGame() {
	rand.Seed(time.Now().UnixNano())
	player1 := NewPlayer("Player1")
	player2 := NewPlayer("Player2")
	players := [2]*Player{&player1, &player2}
	turn := 1
	currentIndex := 0

	for {
		attacker := players[currentIndex]
		defender := players[1-currentIndex]
		troop := attacker.Troops[(turn-1)%3]
		fmt.Printf("\n Turn %d: %s's %s is attacking...\n", turn, attacker.Username, troop.Name)
		log, gameOver := performAttack(troop, defender)
		fmt.Print(log)
		if gameOver {
			break
		}
		currentIndex = 1 - currentIndex
		time.Sleep(1 * time.Second)
		turn++
	}
}

func main() {
	simulateGame()
}
