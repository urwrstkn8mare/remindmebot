package main

import (
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/araddon/dateparse"
	"github.com/bwmarrin/discordgo"
	"github.com/go-co-op/gocron"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/raintank/dur"
)

func remind(mention, msg, from string, tts bool, channel string, last bool, sess *discordgo.Session, sched *gocron.Scheduler, job gocron.Job) {
	var components []discordgo.MessageComponent

	if last {
		updateStatus(-1, sess)
	} else {
		tag := job.Tags()[0]
		customId := tag + strconv.Itoa(job.RunCount())
		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Remove reminder",
						Style:    discordgo.DangerButton,
						CustomID: customId,
						Disabled: false,
					},
				},
			},
		}
		sess.AddHandlerOnce(func(sess *discordgo.Session, m *discordgo.InteractionCreate) {
			if m.MessageComponentData().CustomID == customId {
				sched.RemoveByTag(tag)
				sess.InteractionRespond(m.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Removed reminder!"}})
				// sess.ChannelMessageSendReply(channel, "Removed reminder!", m.Message.Reference())
				log.Info("Reminder removed!")
			}
		})
	}
	_, err := sess.ChannelMessageSendComplex(
		channel,
		&discordgo.MessageSend{
			Content:    mention + " **" + msg + "** (courtesy of " + from + ")",
			TTS:        tts,
			Components: components,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Info("Sent reminder!")
}

var reminderCount int

func updateStatus(diff int, sess *discordgo.Session) {
	reminderCount += diff
	sess.UpdateGameStatus(0, strconv.Itoa(reminderCount)+" reminders!")
}

type Config struct {
	Token string `yaml:"token"`
}

var cfg Config

func main() {
	log.Info("Getting config...")
	err := cleanenv.ReadConfig("config.yml", &cfg)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Creating bot...")

	discord, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		log.Fatal(err)
	}
	updateStatus(0, discord)

	log.Info("Creating scheduler...")
	sched := gocron.NewScheduler(time.Local)
	sched.WaitForScheduleAll()

	log.Info("Adding discord bot handlers...")
	discord.AddHandler(func(_ *discordgo.Session, _ *discordgo.RateLimit) {
		log.Warn("Bot is being rate limited!")
	})

	discord.AddHandler(func(sess *discordgo.Session, m *discordgo.MessageCreate) {
		if len(m.Content) > 6 && m.Content[:6] == "remind" {
			log.Info("Reminder received: ", m.Content)

			fail := func(msg string) {
				sess.ChannelMessageSendReply(m.ChannelID, msg, m.Reference())
				log.Info("Couldn't understand, no reminder set.")
			}

			words := strings.Split(m.Content, " ")

			if len(words) < 2 {
				fail("??? Try again!")
				return
			}

			var mention string
			switch words[1] {
			case "me":
				mention = m.Author.Mention()
			case "everyone":
				mention = "@everyone"
			case "him", "her", "them":
				mention = m.ReferencedMessage.Author.Mention()
			default:
				if words[1][0] == '@' {
					mention = words[1]
				} else {
					fail("Sorry remind who? Try again!")
					return
				}
			}

			keys := make(map[string]string)
			var cur string
			var check int8
			for _, el := range words[1:] {
				switch el {
				case "at", "in":
					check += 2
					cur = el
				case "every", "from":
					check += 1
					cur = el
				case "to", "with":
					cur = el
				default:
					keys[cur] += el + " "
				}
				if check > 2 {
					fail("Can't understand! A very weird combination of keywords, try again!")
					return
				}
			}

			var at, from time.Time
			var every, in uint32
			var msg string
			tts := false
			for key, val := range keys {
				val = strings.TrimSpace(val)
				switch key {
				case "at":
					at, err = dateparse.ParseAny(val, dateparse.PreferMonthFirst(false))
					if err != nil {
						log.Warn(err)
						fail("Can't understand the 'at' date/time. Try again!")
						return
					}
				case "from":
					from, err = dateparse.ParseAny(val, dateparse.PreferMonthFirst(false))
					if err != nil {
						log.Warn(err)
						fail("Can't understand the 'from' date/time. Try again!")
						return
					}
				case "every":
					every, err = dur.ParseDuration(val)
					if err != nil {
						log.Warn(err)
						fail("Can't understand the 'every' date/time. Try again!")
						return
					}
				case "in":
					in, err = dur.ParseDuration(val)
					if err != nil {
						log.Warn(err)
						fail("Can't understand the 'in' date/time. Try again!")
						return
					}
				case "to":
					msg = val
				case "with":
					tts = strings.Contains(val, "tts")
				}
			}
			if msg == "" {
				fail("What's the message??? Try again!")
				return
			}
			if at.IsZero() && every == 0 && from.IsZero() && in == 0 {
				fail("When??? Try again!")
				return
			}

			var job *gocron.Job
			if at.IsZero() && in == 0 {
				s := sched.Every(time.Duration(every) * time.Second).WaitForSchedule()
				if !from.IsZero() {
					s = s.StartAt(from)
				}
				job, err = s.DoWithJobDetails(remind, mention, msg, m.Author.Mention(), tts, m.ChannelID, false, discord, sched)
			} else {
				// s := sched.StartAt(time.Now())
				s := sched.WaitForSchedule()
				if in == 0 {
					s = s.Every(time.Until(at))
				} else {
					s = s.Every(time.Duration(in) * time.Second)
				}
				job, err = s.LimitRunsTo(1).DoWithJobDetails(remind, mention, msg, m.Author.Mention(), tts, m.ChannelID, true, discord, sched)
			}

			job.Tag(m.ID)

			if err != nil {
				fail("Error with scheduler. Let the creator know!!")
				log.Fatal(err)
				return
			}
			updateStatus(1, discord)
			sess.ChannelMessageSendReply(m.ChannelID, "Reminder set! Next reminder at: "+job.NextRun().Local().Format(time.UnixDate), m.Reference())
			log.Info("Reminder set!")
		}
	})

	log.Info("Opening websocket connection to Discord.")
	discord.Open()

	log.Info("Starting scheduler...")
	sched.StartBlocking()
}
