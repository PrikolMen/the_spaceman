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

func main() {
	db, err := sql.Open("sqlite3", "./store.db")
	if err != nil {
		fmt.Println("error opening database,", err)
		return
	}

	db.Exec("CREATE TABLE IF NOT EXISTS channels (channelID TEXT PRIMARY KEY)")

	session, err := discordgo.New("Bot " + Token)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	result, err := db.Query("SELECT * FROM channels")
	if err == nil {
		var channelID string
		for result.Next() {
			err := result.Scan(&channelID)
			if err != nil {
				fmt.Println("sql data reading error:", err)
			} else {
				CreatedChannels = append(CreatedChannels, channelID)
			}
		}
	}

	_, err = db.Exec("DELETE FROM channels")
	if err != nil {
		fmt.Println("sql delete error:", err)
	}

	db.Close()

	session.AddHandler(initialized)
	session.AddHandler(textChatEvent)
	session.AddHandler(voiceChatEvent)

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates

	err = session.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	fmt.Println("Connection established.  Press CTRL-C to exit.")

	// CTRL-C waiting for exit
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	session.Close()
}

var VoiceChannels = make(map[string]int32)

func initialized(session *discordgo.Session, ready *discordgo.Ready) {
	CreatedChannels = slices.DeleteFunc(CreatedChannels, func(channelID string) bool {
		if VoiceChannels[channelID] == 0 {
			db, err := sql.Open("sqlite3", "./store.db")
			if err != nil {
				fmt.Println("error opening database,", err)
			} else {
				db.Exec("DELETE FROM channels WHERE channelID = (?)", channelID)
				db.Close()
			}

			session.ChannelDelete(channelID)

			channel, err := session.Channel(channelID)
			if err != nil {
				fmt.Printf("Voice channel 'unknown ( %s )' has been deleted.\n", channelID)
			} else {
				fmt.Printf("Voice channel '%s ( %s )' has been deleted.\n", channel.Name, channelID)
			}
			return true
		}

		return false
	})

	for _, guild := range session.State.Guilds {
		for _, voiceState := range guild.VoiceStates {
			channelID := voiceState.ChannelID
			VoiceChannels[channelID]++

			if AllowedChannels[channelID] {
				user, err := session.GuildMember(voiceState.GuildID, voiceState.UserID)
				if err != nil {
					fmt.Println("error getting guild member,", err)
					continue
				}

				channel, err := session.Channel(channelID)
				if err != nil {
					fmt.Println("error getting channel,", err)
					continue
				}

				createRoom(session, voiceState.GuildID, user.User, channel.ParentID, channel.Position)
			}
		}
	}

	session.UpdateGameStatus(0, "Garry's Mod")
}

func textChatEvent(session *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	botID := session.State.User.ID
	for _, mention := range m.Mentions {
		if mention.ID != botID {
			continue
		}

		content, _ := strings.CutPrefix(m.Content, "<@"+botID+">")
		content = strings.TrimSpace(content)

		if strings.HasPrefix(content, "ping me") {
			session.ChannelMessageSend(m.ChannelID, "Hey <@"+m.Author.ID+">!")
		}
	}
}

const channelPermissions int64 = discordgo.PermissionManageChannels | discordgo.PermissionVoiceMoveMembers

func createRoom(session *discordgo.Session, guildID string, user *discordgo.User, parentID string, position int) {
	channel, err := session.GuildChannelCreate(guildID, fmt.Sprintf(RoomPattern, user.GlobalName), discordgo.ChannelTypeGuildVoice)
	if err != nil {
		fmt.Println("error creating voice channel,", err)
		return
	}

	channelID := channel.ID
	session.ChannelEdit(channelID, &discordgo.ChannelEdit{
		Position: &position,
		ParentID: parentID,
	})

	CreatedChannels = append(CreatedChannels, channelID)

	db, err := sql.Open("sqlite3", "./store.db")
	if err != nil {
		fmt.Println("error opening database,", err)
		return
	}

	db.Exec("INSERT INTO channels VALUES (?)", channelID)
	db.Close()

	fmt.Printf("Voice channel '%s ( %s )' has been created for '%s'\n", channel.Name, channelID, user.GlobalName)

	err = session.GuildMemberMove(guildID, user.ID, &channelID)
	if err != nil {
		fmt.Println("error moving user to voice channel,", err)
		return
	}

	session.ChannelPermissionSet(channelID, user.ID, discordgo.PermissionOverwriteTypeMember, channelPermissions, 0)
}

func voiceChatEvent(session *discordgo.Session, voiceState *discordgo.VoiceStateUpdate) {
	if voiceState.BeforeUpdate != nil {
		channelID := voiceState.BeforeUpdate.ChannelID

		userCount := VoiceChannels[channelID]
		if userCount > 0 {
			userCount = userCount - 1
		}

		VoiceChannels[channelID] = userCount

		if userCount == 0 && slices.Contains(CreatedChannels, channelID) {
			channel, err := session.Channel(channelID)
			if err == nil {
				db, err := sql.Open("sqlite3", "./store.db")
				if err != nil {
					fmt.Println("error opening database,", err)
				} else {
					db.Exec("DELETE FROM channels WHERE channelID = (?)", channelID)
					db.Close()
				}

				session.ChannelDelete(channelID)
				fmt.Printf("Voice channel '%s ( %s )' has been deleted.\n", channel.Name, channelID)
			}
		}
	}

	channelID := voiceState.ChannelID
	if len(channelID) == 0 {
		return
	}

	userCount := VoiceChannels[channelID] + 1
	VoiceChannels[channelID] = userCount

	if AllowedChannels[channelID] {
		channel, err := session.Channel(channelID)
		if err != nil {
			fmt.Println("error getting channel,", err)
			return
		}

		createRoom(session, voiceState.GuildID, voiceState.Member.User, channel.ParentID, channel.Position)
	}
}
