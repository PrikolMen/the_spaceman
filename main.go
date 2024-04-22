package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"
	_ "github.com/mattn/go-sqlite3"
)

var (
	CreatedChannels []string
	RoomPattern     string
	Token           string
)

var AllowedChannels = make(map[string]bool)

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	channels := flag.String("c", "", "ChannelIDs")
	flag.StringVar(&RoomPattern, "rp", "%s's Room", "Room Suffix")
	flag.Parse()

	for _, channelID := range strings.Split(*channels, " ") {
		AllowedChannels[channelID] = true
	}
}

// Count of users in each voice channel
var UserCount = make(map[string]uint16)

func main() {
	// Connecting to the database
	db, err := sql.Open("sqlite3", "./store.db")
	if err != nil {
		fmt.Println("Error on opening database,", err)
		return
	}

	// Creating database table if not exists
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS channels (channelID TEXT PRIMARY KEY)")
	if err != nil {
		fmt.Println("Error on creating database table,", err)
		return
	}

	// Creating a new discord bot session
	session, err := discordgo.New("Bot " + Token)
	if err != nil {
		fmt.Println("Error on creating Discord session,", err)
		return
	}

	// Reading all early created channels
	result, err := db.Query("SELECT * FROM channels")
	if err == nil {
		var channelID string
		for result.Next() {
			err := result.Scan(&channelID)
			if err != nil {
				fmt.Println("SQL data reading error,", err)
			} else {
				CreatedChannels = append(CreatedChannels, channelID)
			}
		}
	}

	// Set intents
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates

	// Ready handler
	session.AddHandler(func(session *discordgo.Session, ready *discordgo.Ready) {
		// Setting funny game status
		session.UpdateGameStatus(0, "Garry's Mod")
	})

	// GuildCreate handler
	session.AddHandler(func(session *discordgo.Session, guild *discordgo.GuildCreate) {
		// Getting guild channels
		guildChannels := make(map[string]bool)

		for _, channel := range guild.Channels {
			guildChannels[channel.ID] = true
		}

		// Checking existing voice channels by voice state
		for _, voiceState := range guild.VoiceStates {
			voiceStateChanged(db, session, voiceState)
		}

		// Removing channels that are no longer in use
		CreatedChannels = slices.DeleteFunc(CreatedChannels, func(channelID string) bool {
			if guildChannels[channelID] && UserCount[channelID] == 0 {
				removeRoom(db, session, channelID)
				return true
			}

			return false
		})
	})

	// Text chat handler
	session.AddHandler(func(session *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.Bot {
			return
		}

		// Bot responds only if it has been mentioned
		botID := session.State.User.ID
		for _, mention := range m.Mentions {
			if mention.ID != botID {
				continue
			}

			content, _ := strings.CutPrefix(m.Content, "<@"+botID+">")
			content = strings.TrimSpace(content)

			// Commands
			if strings.HasPrefix(content, "ping me") {
				session.ChannelMessageSend(m.ChannelID, "Hey <@"+m.Author.ID+">!")
			}
		}
	})

	// Voice chat handler
	session.AddHandler(func(session *discordgo.Session, voiceState *discordgo.VoiceStateUpdate) {
		// Previous user voice state
		beforeUpdate := voiceState.BeforeUpdate
		if beforeUpdate != nil {
			channelID := beforeUpdate.ChannelID
			if !slices.Contains(CreatedChannels, channelID) {
				return
			}

			// Counting users in last voice channel
			userCount := UserCount[channelID]
			if userCount > 0 {
				userCount = userCount - 1
				UserCount[channelID] = userCount
			}

			// Deleting room if no users in it
			if userCount == 0 {
				removeRoom(db, session, channelID)
			}
		}

		voiceStateChanged(db, session, voiceState.VoiceState)
	})

	// Opening websocket connection
	err = session.Open()
	if err != nil {
		fmt.Println("Error opening connection,", err)
		return
	}

	// Wait here until CTRL-C or other term signal is received
	fmt.Println("Connection established.  Press CTRL-C to exit.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Disconnect from Discord and close database
	session.Close()
	db.Close()
}

func voiceStateChanged(db *sql.DB, session *discordgo.Session, voiceState *discordgo.VoiceState) {
	channelID := voiceState.ChannelID
	if len(channelID) == 0 {
		return
	}

	// Counting users
	if slices.Contains(CreatedChannels, channelID) {
		UserCount[channelID]++
		return
	}

	// Checking if channel is allowed
	if !AllowedChannels[channelID] {
		return
	}

	// Creating room if user in allowed channel
	user, err := session.User(voiceState.UserID)
	if err != nil {
		fmt.Println("Error getting voice chat user,", err)
		return
	}

	channel, err := session.Channel(channelID)
	if err != nil {
		fmt.Println("Error getting parent channel,", err)
		return
	}

	createRoom(db, session, voiceState.GuildID, user, channel.ParentID, channel.Position)
}

// Voice chat permissions for creator
const ChannelPermissions int64 = discordgo.PermissionManageChannels | discordgo.PermissionVoiceMoveMembers

// Voice chat creation
func createRoom(db *sql.DB, session *discordgo.Session, guildID string, user *discordgo.User, parentID string, position int) {
	channel, err := session.GuildChannelCreate(guildID, fmt.Sprintf(RoomPattern, user.GlobalName), discordgo.ChannelTypeGuildVoice)
	if err != nil {
		fmt.Printf("Error creating voice channel for user '%s', %s\n", user.GlobalName, err)
		return
	}

	channelID := channel.ID

	// Adding channel to list of created channels
	CreatedChannels = append(CreatedChannels, channelID)

	// Saving channel id to database
	if db != nil {
		_, err = db.Exec("INSERT INTO channels VALUES (?)", channelID)
		if err != nil {
			fmt.Printf("Error saving channel '%s' to database, %s\n", channelID, err)
			removeRoom(db, session, channelID)
			return
		}
	}

	fmt.Printf("Voice channel '%s ( %s )' has been created for '%s'\n", channel.Name, channelID, user.GlobalName)

	// Setting new voice channel position
	session.ChannelEdit(channelID, &discordgo.ChannelEdit{
		Position: &position,
		ParentID: parentID,
	})

	userID := user.ID

	// Moving user into new voice channel
	err = session.GuildMemberMove(guildID, userID, &channelID)
	if err != nil {
		fmt.Printf("Error moving user '%s' into voice channel '%s', %s\n", userID, channelID, err)
		return
	}

	// Setting voice chat permissions for creator
	err = session.ChannelPermissionSet(channelID, userID, discordgo.PermissionOverwriteTypeMember, ChannelPermissions, 0)
	if err != nil {
		fmt.Printf("Error setting voice chat '%s' permissions, for '%s', %s\n", channelID, userID, err)
	}
}

// Remove room
func removeRoom(db *sql.DB, session *discordgo.Session, channelID string) {
	if db != nil {
		_, err := db.Exec("DELETE FROM channels WHERE channelID = (?)", channelID)
		if err != nil {
			fmt.Printf("Error deleting channel id '%s' from database, %s\n", channelID, err)
		}
	}

	channelName := "unknown"

	channel, _ := session.Channel(channelID)
	if channel != nil {
		channelName = channel.Name
	}

	_, err := session.ChannelDelete(channelID)
	if err != nil {
		fmt.Printf("Error voice channel '%s ( %s )' deletion, %s\n", channelName, channelID, err)
	} else {
		fmt.Printf("Voice channel '%s ( %s )' has been deleted.\n", channelName, channelID)
	}

	CreatedChannels = slices.DeleteFunc(CreatedChannels, func(channel string) bool {
		return channel == channelID
	})
}
