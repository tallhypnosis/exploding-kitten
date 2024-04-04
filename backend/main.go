package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"

	l "backend/cardgenerator"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var (
	rdb *redis.Client
)

func init() {
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
}

type LeaderboardEntry struct {
	UserName  string `json:"userName"`
	UserScore int    `json:"userScore"`
}

type GameData struct {
	Score         int    `json:"score"`
	GameCards     []Card `json:"gameCards"`
	HasDefuseCard bool   `json:"hasDefuseCard"`
	ActiveCard    *Card  `json:"acitveCard"`
}

type Card struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

var broadcastLeaderboard = make(chan []byte)

func getLatestLeaderboard() ([]LeaderboardEntry, error) {
	ctx := context.Background()

	leaderboard, err := rdb.ZRevRangeWithScores(ctx, "leaderboard", 0, -1).Result()
	if err != nil {
		return nil, err
	}

	formattedLeaderboard := make([]LeaderboardEntry, 0)

	for _, z := range leaderboard {
		userName := z.Member.(string)
		userScore := int(z.Score)
		formattedLeaderboard = append(formattedLeaderboard, LeaderboardEntry{
			UserName:  userName,
			UserScore: userScore,
		})
	}

	return formattedLeaderboard, nil
}

var (
	upgrader       = websocket.Upgrader{}
	leaderboardMap = make(map[string]int)
	mu             sync.Mutex
)

func websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println("Error upgrading to WebSocket:", err)
		return
	}
	defer conn.Close()

	log.Println("WebSocket connection established")

	for {
		// read message from the client
		messageType, message, err := conn.ReadMessage()

		if err != nil {
			log.Println("Error reading message:", err)
			break
		}

		log.Printf("Received message: %s", message)

		// Update leaderboard
		updateLeaderboard(string(message))

		// Get the latest leaderboard
		leaderboard, err := getLatestLeaderboard()
		if err != nil {
			log.Println("Error fetching latest leaderboard:", err)
			continue
		}

		// Emit the latest leaderboard to all connected clients
		emitLeaderboard(conn, leaderboard, messageType)

	}
}

func updateLeaderboard(userName string) {
	mu.Lock()
	defer mu.Unlock()

	// Increment the score of the user
	leaderboardMap[userName]++
}

func emitLeaderboard(conn *websocket.Conn, leaderboard []LeaderboardEntry, messageType int) {
	// Serialize the leaderboard to JSON
	leaderboardJSON, err := json.Marshal(leaderboard)
	if err != nil {
		log.Println("Error serializing leaderboard:", err)
		return
	}

	// Send the serialized  leaderboard to the client
	if err := conn.WriteMessage(messageType, leaderboardJSON); err != nil {
		log.Println("Error writing leaderboard:", err)
	}
}

func gameHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	userName := r.URL.Query().Get("userName")

	// Check if the user exists
	ctx := context.Background()
	isMemberCmd := rdb.Exists(ctx, userName)
	isMember, err := isMemberCmd.Result()
	if err != nil {
		http.Error(w, "Failed to check if the user exists", http.StatusInternalServerError)
		return
	}

	// Initialize game data for new users
	if isMember == 0 && userName != "" {
		randomCards := l.GenerateRandomCards()
		randomCardsJSON, err := json.Marshal(randomCards)
		if err != nil {
			http.Error(w, "Failed to initialize game data", http.StatusInternalServerError)
		}
		_, err = rdb.HMSet(ctx, userName, map[string]interface{}{
			"score":         0,
			"gameCards":     randomCardsJSON,
			"hasDefuseCard": false,
			"activeCard":    nil,
		}).Result()
		if err != nil {
			http.Error(w, "Failed to initiate game for new user", http.StatusInternalServerError)
			return
		}
		rdb.ZAdd(ctx, "leaderboard", &redis.Z{Score: 0, Member: userName})
	}

	// Retrieve game data for the user
	gameData := GameData{}
	hgetallCmd := rdb.HGetAll(ctx, userName)
	gameDataMap, err := hgetallCmd.Result()
	if err != nil {
		http.Error(w, "Failed to retrieve user data", http.StatusInternalServerError)
		return
	}
	gameData.Score, _ = strconv.Atoi(gameDataMap["score"])
	json.Unmarshal([]byte(gameDataMap["gameCards"]), &gameData.GameCards)
	gameData.HasDefuseCard, _ = strconv.ParseBool((gameDataMap["hasDefuseCard"]))
	if activeCardValue, ok := gameDataMap["activeCard"]; ok && activeCardValue != "" {
		gameData.ActiveCard = &Card{Name: activeCardValue}
	}

	// Emit the latest leaderboard (not implemented in this code snippet)
	leaderboard, err := getLatestLeaderboard()
	if err != nil {
		http.Error(w, "Failed to retrieve latest leaderboard", http.StatusInternalServerError)
		return
	}

	// Send the game data and latest leaderboard as response
	responseData := struct {
		GamdeData   GameData           `json:"gameData"`
		LeaderBoard []LeaderboardEntry `json:"leaderboard"`
	}{
		GamdeData:   gameData,
		LeaderBoard: leaderboard,
	}

	// Sent the game data as response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}

func updateGameData(w http.ResponseWriter, r *http.Request) {
	// Parse request Body
	var requestBody map[string]interface{}
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	// Extract values from request body
	userName, _ := requestBody["userName"].(string)
	hasDefuseCard, _ := requestBody["hasDefuseCard"].(bool)
	activeCard, _ := requestBody["activeCard"].(string)
	score, _ := strconv.Atoi(requestBody["score"].(string))

	gameCards := requestBody["gameCards"].([]interface{})
	gameCardsStr, _ := json.Marshal((gameCards))

	// Update game data in Redis
	ctx := context.Background()
	_, err = rdb.HMSet(ctx, userName,
		"gameCards", string(gameCardsStr),
		"hasDefuseCard", hasDefuseCard,
		"activeCard", activeCard,
		"score", score,
	).Result()
	if err != nil {
		http.Error(w, "Failed to update game data", http.StatusInternalServerError)
		return
	}

	// Update user's score in the leaderboard
	rdb.ZAdd(ctx, "leaderboard", &redis.Z{Score: float64(score), Member: userName})

	// emit the latest leaderboard
	leaderboard, err := getLatestLeaderboard()
	if err != nil {
		http.Error(w, "Failed to retrieve latest leaderboard", http.StatusInternalServerError)
		return
	}

	// Serialize the leaderboard to JSON
	leaderboardJSON, err := json.Marshal(leaderboard)
	if err != nil {
		http.Error(w, "Failed to serialize leaderboard", http.StatusInternalServerError)
		return
	}

	// Set the appropriate content type header
	w.Header().Set("Content-Type", "application/json")

	// Write the leaderboard JSON as the response
	w.Write(leaderboardJSON)

	// Send response
	responseData := map[string]interface{}{
		"userName":      userName,
		"hadDefuseCard": hasDefuseCard,
		"activeCard":    activeCard,
		"score":         score,
		"gameCards":     gameCards,
	}
	json.NewEncoder(w).Encode(responseData)
}

func resetGame(w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var requestBody map[string]string
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		http.Error(w, "Failed to decode request body", http.StatusBadRequest)
		return
	}

	// Extract userName from request body
	userName := requestBody["userName"]
	if userName == "" {
		http.Error(w, "Missing userName in request body", http.StatusBadRequest)
		return
	}

	// Clear game data for the specified user
	ctx := context.Background()
	_, err = rdb.HMSet(ctx, userName,
		"gameCards", "[]",
		"hasDefuseCard", "false",
		"activeCard", nil,
		"score", 0,
	).Result()
	if err != nil {
		http.Error(w, "Failed to reset game data", http.StatusInternalServerError)
		return
	}

	// Remove user from leaderboard
	rdb.ZRem(ctx, "leaderboard", userName)

	// Emit the latest leaderboard
	leaderboard, err := getLatestLeaderboard()
	if err != nil {
		http.Error(w, "Failed to retrieve latest leaderboard", http.StatusInternalServerError)
		return
	}

	// Serialize the leaderboard to JSON
	leaderboardJSON, err := json.Marshal(leaderboard)
	if err != nil {
		http.Error(w, "Failed to serialize leaderboard", http.StatusInternalServerError)
		return
	}

	// Write the leaderboard JSON as the response
	w.Header().Set("Content-Type", "application/json")
	w.Write(leaderboardJSON)

	// Emit the latest leaderboard to WebSocket clients
	broadcastLeaderboard <- leaderboardJSON
}

func main() {
	// Initialize the Gorilla router
	r := mux.NewRouter()

	// Register API handlers
	r.HandleFunc("/game", gameHandler).Methods("GET")
	r.HandleFunc("/game", updateGameData).Methods("PUT")
	r.HandleFunc("/game", resetGame).Methods("DELETE")

	// Register WebSocket handler
	http.HandleFunc("/ws", websocketHandler)

	// Start the HTTP server
	server := &http.Server{
		Addr:    ":3000",
		Handler: r,
	}
	fmt.Println("App is running on port 3000")
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
