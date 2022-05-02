package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func tweet(s string) ([]byte, error) {
	fmt.Println(s)
	client := http.Client{
		Timeout: time.Second * 2, // Timeout after 2 seconds
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.twitter.com/2/tweets", nil)
	if err != nil {
		fmt.Println("AHHHH")
		log.Fatal(err)
	}
	fmt.Println("1")

	req.Header.Set("Authorization", "AAAAAAAAAAAAAAAAAAAAABaKcAEAAAAAjOfhGgxlNxolQq27n8sf%2FE8aue4%3DXYANE03WG6dj1Da0c1pRCfPZJISzwSaQlSzJvyAXrJLpSaK7Ox")

	fmt.Println("1a")
	res, postErr := client.Do(req)
	fmt.Println("2")
	if postErr != nil {
		fmt.Println("3")
		fmt.Println("AHHHH")
		fmt.Sprintf("%+v", postErr)
		log.Fatal(postErr)
		return make([]byte, 0), postErr
	}
	fmt.Println("4")

	fmt.Sprintf("%+v", res)

	if res.Body != nil {
		defer res.Body.Close()
	}

	fmt.Println("HIT")
	return ioutil.ReadAll(res.Body)
}

// call the mlb api (or well... the endpoint)
func callAPI(url string) ([]byte, error) {
	client := http.Client{
		Timeout: time.Second * 2, // Timeout after 2 seconds
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Fatal(err)
	}

	res, getErr := client.Do(req)
	if getErr != nil {
		return make([]byte, 0), getErr
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	return ioutil.ReadAll(res.Body)
}

// parse a scoreboard for gamePks
func parseScoreboardData(gameDay time.Time) (ScheduleData, error) {
	data := ScheduleData{}

	body, err := callAPI(fmt.Sprintf("https://bdfed.stitch.mlbinfra.com/bdfed/transform-mlb-scoreboard?stitch_env=prod&sortTemplate=4&sportId=1&startDate=%v&endDate=%v&gameType=E&&gameType=S&&gameType=R&&gameType=F&&gameType=D&&gameType=L&&gameType=W&&gameType=A&language=en&leagueId=104&&leagueId=103&contextTeamId=", gameDay.Format("2006-01-02"), gameDay.Format("2006-01-02")))

	if err != nil {
		return data, err
	}

	jsonErr := json.Unmarshal(body, &data)

	if jsonErr != nil {
		return data, jsonErr
	}

	return data, nil
}

// save a game state to the "cache"
func (g GameCache) saveToFile(game ScheduleGame) error {
	marshalledGame, _ := json.Marshal(g)

	return ioutil.WriteFile(fmt.Sprintf("%v/%v.json", game.GameDate.Format("2006-01-02"), game.GamePk), marshalledGame, 0666)
}

// build a string representing the team's score during a game
// func getScoreString(t GameTeam) string {
// 	return fmt.Sprintf("%v - %v", t.Team.ClubName, t.Score)
// }

// build a string representing the pitcher's statline for the game
func getPitcherStatLine(p *BoxscorePlayer) string {
	s := []string{}

	stats := p.Stats.Pitching
	s = append(s, fmt.Sprintf("%vIP", stats.InningsPitched), fmt.Sprintf("%vH", stats.Hits), fmt.Sprintf("%vBB", stats.BaseOnBalls))

	if stats.HitByPitch != 0 {
		s = append(s, fmt.Sprintf("%vHBP", stats.HitByPitch))
	}

	if stats.HomeRuns != 0 {
		s = append(s, fmt.Sprintf("%vHR", stats.HomeRuns))
	}

	s = append(s, fmt.Sprintf("%vER", stats.EarnedRuns), fmt.Sprintf("%v Pitches", stats.NumberOfPitches))

	return strings.Join(s, ", ")
}

// build a string representing the game score with the higher scoring team being first
func getGameScoreString(g Boxscore) string {
	away := g.Teams.Away
	home := g.Teams.Home

	awayScore := away.TeamStats.Batting.Runs
	homeScore := home.TeamStats.Batting.Runs

	return fmt.Sprintf("%v - %v @ %v - %v", away.Team.Name, awayScore, home.Team.Name, homeScore)
}

func loadCacheForGame(game ScheduleGame) (GameCache, error) {
	cache := GameCache{}
	filename := fmt.Sprintf("%v/%v.json", game.GameDate.Format("2006-01-02"), game.GamePk)

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		fmt.Printf("New game: %v, will need to create a new cache\n", game.GamePk)
	} else {
		jsonData, _ := os.Open(filename)
		defer jsonData.Close()

		jsonBytes, _ := ioutil.ReadAll(jsonData)
		jsonErr := json.Unmarshal(jsonBytes, &cache)

		if jsonErr != nil {
			// TODO: better error handling
			return cache, jsonErr
		}
	}

	return cache, nil
}

func (gc GameCache) containsPitcher(key string) bool {
	for _, k := range gc.PitchedKeys {
		if k == key {
			return true
		}
	}

	return false
}

// parse a particular game
func parseGameData(game ScheduleGame, c chan CloserNews) {
	data := GameData{}

	if time.Now().Before(game.GameDate) {
		// fmt.Println("Game Hasn't Started")
		c <- CloserNews{
			Game: game,
		}
		return
	}

	gameId := game.GamePk
	cache, cacheErr := loadCacheForGame(game)

	if nil != cacheErr {
		fmt.Printf("---%v\n", game.GamePk)
		fmt.Println("Error1:", cacheErr)
		os.Exit(1)
	}

	body, _ := callAPI(fmt.Sprintf("https://statsapi.mlb.com/api/v1.1/game/%v/feed/live?language=en", gameId))

	// TODO : fuck with errors later
	// if err != nil {
	// 	return data, err
	// }

	jsonErr := json.Unmarshal(body, &data)

	if jsonErr != nil {
		// return data, jsonErr
		// TODO : fuck with errors later
	}

	box := data.LiveData.Boxscore

	// fmt.Printf("CHECKING %v @ %v\n", box.Teams.Away.Team.Name, box.Teams.Home.Team.Name)

	// if top of inning need to look at home team pitchers
	// if bottom look at away team pitchers
	// we can sort that out later

	players := box.Teams.Home.Players
	for k, v := range box.Teams.Away.Players {
		players[k] = v
	}

	for k, v := range players {
		if 0 < v.Stats.Pitching.GamesPlayed && "" != v.Stats.Pitching.Note && 0 == v.Stats.Pitching.GamesStarted && !cache.containsPitcher(k) { // && !v.GameStatus.IsCurrentPitcher {
			cache.Status = data.GameData.Status.AbstractGameCode
			cache.PitchedKeys = append(cache.PitchedKeys, k)

			stat := ""
			count := 0
			switch 1 {
			case v.Stats.Pitching.Saves:
				stat = "Save"
				count = v.SeasonStats.Pitching.Saves
			case v.Stats.Pitching.Holds:
				stat = "Hold"
				count = v.SeasonStats.Pitching.Holds
			case v.Stats.Pitching.BlownSaves:
				stat = "Blown Save"
				count = v.SeasonStats.Pitching.BlownSaves
			}

			c <- CloserNews{
				Tweet: fmt.Sprintf("%v (%v) - %v (%v)\n%v\n\nFinal: %v", v.Person.FullName, "TEAM", stat, count, getPitcherStatLine(&v), getGameScoreString(box)),
				Game:  game,
			}
		}
	}

	// save to file
	writeErr := cache.saveToFile(game)

	if nil != writeErr {
		// 	return data, writeErr
	}

	// return data, nil
	c <- CloserNews{
		Game: game,
	}
}

func main() {
	gameDay := time.Now()
	gameDay = gameDay.AddDate(0, 0, -1)
	data, err := parseScoreboardData(gameDay)

	games := []ScheduleGame{}
	if err != nil {
		fmt.Printf("Error getting games for day %v", gameDay.Format("Mon Jan 02 2006"))
		log.Fatal(err)
	}

	// commented out because it breaks everything
	// if 0 == data.Dates[0].TotalGamesInProgress {
	// 	os.Exit(1)
	// }

	if _, err := os.Stat(gameDay.Format("2006-01-02")); os.IsNotExist(err) {
		// path/to/whatever does not exist
		e := os.Mkdir(gameDay.Format("2006-01-02"), 0755)
		if e != nil {
			panic(e)
		}
	}

	for _, g := range data.Dates[0].Games {
		games = append(games, g)
		// OLD SHIT
		// filename := fmt.Sprintf("%v/%v", gameDay.Format("2006-01-02"), g.GamePk)
		// if _, err := os.Stat(filename); os.IsNotExist(err) {
		// 	// file doesn't exist -- check to see if we need to write it
		// 	if "F" == g.Status.AbstractGameCode {
		// 		// game is final -- check for save
		// 		if nil != g.Decisions.Save {
		// 			// tweet out the saver of the game w/ score
		// 			fmt.Printf("Save: %v (%v)\n%v\n\nFinal: %v\n\n\n", g.Decisions.Save.FullName, g.Decisions.Save.Stats[3].Stats.Saves, getPitcherStatLine(g.Decisions.Save), getGameScoreString(g))
		// 		}

		// 		// write this pk to disk so we dont send it out again
		// 		fileWriteErr := ioutil.WriteFile(filename, []byte(""), 0666)

		// 		if nil != fileWriteErr {
		// 			log.Fatal(err)
		// 		}

		// 	}
		// }
	}

	numGames := len(games)

	c := make(chan CloserNews)

	for _, game := range games {
		go parseGameData(game, c)
	}

	for news := range c {
		go func(n CloserNews) {
			if "" != n.Tweet {
				// fmt.Println(n.Tweet)
				_, err := tweet(n.Tweet)

				if nil != err {
					time.Sleep(7 * time.Second)
					fmt.Println("EFFF")
					fmt.Println(err)
				}
			}

			// fmt.Println(n.Game.Status.AbstractGameCode)
			if "F" != n.Game.Status.AbstractGameCode {
				time.Sleep(time.Second * 10)
				parseGameData(n.Game, c)
			} else {
				numGames--

				if numGames <= 0 {
					close(c)
				}
			}
		}(news)
	}

	// close(c)
	fmt.Println("Done for the day!!")

	/**
	// http.HandleFunc("/", homePage)
	// log.Fatal(http.ListenAndServe(":8081", nil))

	// TODO: i could pull each of the games in, and use the individual game url in a goroutine for each game. as a game ends, i could "close" the channel?
	// better or worse than an intermittent lambda fucntion?

	if gameDay.Hour() < 23 { // TODO: this will be a different number when i want to actually run this thing for a day or two straight
		gameDay = gameDay.AddDate(0, 0, -1)
	}



	data, err := parseScoreboardData(gameDay)

	if nil != err {
		log.Fatal(err)
	}
	**/
}

// TODO - holds, blown saves
