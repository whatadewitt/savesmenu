package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/dghubble/oauth1"
	"github.com/joho/godotenv"
)

func tweet(s string) ([]byte, error) {
	config := oauth1.NewConfig(os.Getenv("TWITTER_API_KEY"), os.Getenv("TWITTER_API_SECRET"))
	token := oauth1.NewToken(os.Getenv("TWITTER_CLIENT_KEY"), os.Getenv("TWITTER_CLIENT_SECRET"))

	client := config.Client(oauth1.NoContext, token)

	fmt.Println(s)

	twitterUrl := "https://api.twitter.com/2/tweets"

	payload := strings.NewReader(fmt.Sprintf(`{ "text": "%v" }`, s))

	fmt.Println(payload)

	res, err := client.Post(twitterUrl, "application/json", payload)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	return body, nil
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
	cfg, err := config.LoadDefaultConfig(context.TODO())

	if err != nil {
		log.Fatal(err)
	}

	fmt.Sprintf("%+v", game)

	client := s3.NewFromConfig(cfg)
	marshalledGame, _ := json.Marshal(g)

	input := &s3.PutObjectInput{
		Bucket: aws.String("savesmenu"),
		Key:    aws.String(fmt.Sprintf(fmt.Sprintf("%v/%v.json", game.GameDate.Format("2006-01-02"), game.GamePk))),
		Body:   bytes.NewReader(marshalledGame),
	}

	_, err = client.PutObject(context.TODO(), input)

	return err
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
	cfg, err := config.LoadDefaultConfig(context.TODO())

	if err != nil {
		log.Fatal(err)
	}

	filename := fmt.Sprintf("%v/%v.json", game.GameDate.Format("2006-01-02"), game.GamePk)

	client := s3.NewFromConfig(cfg)
	params := &s3.GetObjectInput{
		Bucket: aws.String("savesmenu"),
		Key:    aws.String(filename),
	}

	resp, err := client.GetObject(context.TODO(), params)

	if nil != err {
		fmt.Printf("New game: %v, will need to create a new cache\n", game.GamePk)
	}

	sz := int(resp.ContentLength)
	buffer := make([]byte, sz)
	defer resp.Body.Close()
	var bbuffer bytes.Buffer
	for true {
		num, rerr := resp.Body.Read(buffer)
		if num > 0 {
			bbuffer.Write(buffer[:num])
		} else if rerr == io.EOF || rerr != nil {
			break
		}
	}

	jsonErr := json.Unmarshal([]byte(bbuffer.String()), &cache)

	if jsonErr != nil {
		// TODO: better error handling
		return cache, jsonErr
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

	// if top of inning need to look at home team pitchers
	// if bottom look at away team pitchers
	// we can sort that out later

	teams := make(map[int]string)
	teams[data.GameData.Teams.Home.ID] = data.GameData.Teams.Home.Abbreviation
	teams[data.GameData.Teams.Away.ID] = data.GameData.Teams.Away.Abbreviation

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
			case v.Stats.Pitching.Losses:
				stat = "Loss"
				count = v.SeasonStats.Pitching.Losses
			}

			inning := data.LiveData.Plays.About.Inning
			halfInning := data.LiveData.Plays.About.HalfInning
			gameInning := fmt.Sprintf("%v %v", halfInning, inning)

			// TODO: check here for game end too
			if 9 >= inning {
				if ("top" == halfInning && box.Teams.Home.TeamStats.Batting.Runs > box.Teams.Away.TeamStats.Batting.Runs) || ("bottom" == halfInning && box.Teams.Home.TeamStats.Batting.Runs != box.Teams.Away.TeamStats.Batting.Runs) {
					gameInning = "Final"
				}
			}

			// TODO: not final...
			c <- CloserNews{
				Tweet: fmt.Sprintf(`%v (%v) - %v (%v)\n%v\n\%v: %v`, v.Person.FullName, teams[v.ParentTeamID], stat, count, getPitcherStatLine(&v), gameInning, getGameScoreString(box)),
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
	godotenv.Load()

	gameDay := time.Now()
	// gameDay = gameDay.AddDate(0, 0, -1)
	data, err := parseScoreboardData(gameDay)

	games := []ScheduleGame{}
	if err != nil {
		fmt.Printf("Error getting games for day %v", gameDay.Format("Mon Jan 02 2006"))
		log.Fatal(err)
	}

	for _, g := range data.Dates[0].Games {
		games = append(games, g)
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
					fmt.Println("EFFF")
					fmt.Println(err)
				}
			}

			if "F" != n.Game.Status.AbstractGameCode {
				time.Sleep(time.Second * 10)
				parseGameData(n.Game, c)
			} else {
				// numGames--

				if numGames <= 0 {
					close(c)
				}
			}
		}(news)
	}

	// close(c)
	fmt.Println("Done for the day!!")

	err = os.Remove(gameDay.Format("2006-01-02"))
	if err != nil {
		panic(err)
	}
}
