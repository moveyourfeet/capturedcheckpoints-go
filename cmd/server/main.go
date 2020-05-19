package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"

	"goji.io"
	"goji.io/pat"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var (
	healthy int32
	cfg     Config
)

// Config for the main command
type Config struct {
	Port          string        `default:"5100"`
	Graceful      time.Duration `default:"5s"`
	LogLevel      string        `default:"info"`
	MongoURL      string        `default:"root:example@localhost:27017"`
	MongoDatabase string        `default:"capturedcheckpoints"`
}

func main() {

	godotenv.Load()

	boolPtr := flag.Bool("help", false, "usage info")
	flag.Parse()

	if *boolPtr {
		envconfig.Usage("CAPTUREDCHECKPOINTS", &cfg)
		return
	}

	err := envconfig.Process("CAPTUREDCHECKPOINTS", &cfg)
	if err != nil {
		log.Fatalf("Error parsing config: %v", err)
	}

	// Setup log
	level, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.Errorf("Error parsing log level: %v", err)
	}
	log.SetLevel(level)

	log.Infof("Connecting to MongoDB: %s", cfg.MongoURL)
	session, err := mgo.Dial(cfg.MongoURL)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	session.SetMode(mgo.Monotonic, true)
	ensureIndex(session)

	mux := goji.NewMux()
	mux.HandleFunc(pat.Get("/races/:Id"), raceByID(session))
	mux.HandleFunc(pat.Put("/races/:Id"), createOrUpdateRace(session))
	mux.HandleFunc(pat.Delete("/races/:Id"), deleteRace(session))
	mux.HandleFunc(pat.Get("/healthz"), healthz)
	mux.Use(LoggingMiddleware())

	// Start server
	log.Infof("Listening on %s", cfg.Port)
	srv := &http.Server{
		Addr: "0.0.0.0:" + cfg.Port,
		// Good practice to set timeouts to avoid Slowloris attacks.
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      mux, // Pass our instance of gorilla/mux in.
	}

	// Run our server in a goroutine so that it doesn't block.
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Errorln(err)
		}
	}()

	// Healthy now
	atomic.StoreInt32(&healthy, 1)

	c := make(chan os.Signal, 1)
	// We'll accept graceful shutdowns when quit via SIGINT (Ctrl+C)
	// SIGKILL, SIGQUIT or SIGTERM (Ctrl+/) will not be caught.
	signal.Notify(c, os.Interrupt)

	// Block until we receive our signal.
	<-c

	// Create a deadline to wait for.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Graceful)
	defer cancel()
	// Doesn't block if no connections, but will otherwise wait
	// until the timeout deadline.
	srv.Shutdown(ctx)
	// Optionally, you could run srv.Shutdown in a goroutine and block on
	// <-ctx.Done() if your application should wait for other services
	// to finalize based on context cancellation.
	log.Info("shutting down")
	os.Exit(0)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if atomic.LoadInt32(&healthy) == 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{\"status\": \"OK\"}"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func ensureIndex(s *mgo.Session) {
	session := s.Copy()
	defer session.Close()

	c := session.DB(cfg.MongoDatabase).C("races")

	index := mgo.Index{
		Key:        []string{"Id"},
		Unique:     true,
		DropDups:   true,
		Background: true,
		Sparse:     true,
	}
	err := c.EnsureIndex(index)
	if err != nil {
		panic(err)
	}
}

func raceByID(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		ID := pat.Param(r, "Id")

		c := session.DB(cfg.MongoDatabase).C("races")

		var race Race
		err := c.Find(bson.M{"Id": ID}).One(&race)
		if err != nil {
			if err.Error() == "not found" {
				race = NewRace(ID)
			}
		}

		respBody, err := json.MarshalIndent(race, "", "  ")
		if err != nil {
			log.Fatal(err)
		}

		responseWithJSON(w, respBody, http.StatusOK)
	}
}

func createOrUpdateRace(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		ID := pat.Param(r, "Id")

		var captured CapturedCheckpoint
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&captured)
		if err != nil {
			log.Println(err)
			errorWithJSON(w, "Incorrect body", http.StatusBadRequest)
			return
		}

		c := session.DB(cfg.MongoDatabase).C("races")

		var race Race
		err = c.Find(bson.M{"Id": ID}).One(&race)
		if err != nil {
			if err.Error() == "not found" {
				race = NewRace(ID)

			} else {
				errorWithJSON(w, "Database error", http.StatusInternalServerError)
				log.Println("Failed find Race: ", err)
				return
			}
		}

		if race.ID == "" {
			errorWithJSON(w, "Race not found", http.StatusNotFound)
			return
		}
		race.CapturedCheckpoints = appendIfMissing(race.CapturedCheckpoints, captured.CapturedCheckpoint)

		_, err = c.Upsert(bson.M{"Id": ID}, &race)
		if err != nil {
			switch err {
			default:
				errorWithJSON(w, "Database error", http.StatusInternalServerError)
				log.Println("Failed update Race: ", err)
				return
			case mgo.ErrNotFound:
				errorWithJSON(w, "Race not found", http.StatusNotFound)
				return
			}
		}

		respBody, err := json.MarshalIndent(race, "", "  ")
		if err != nil {
			log.Fatal(err)
		}

		responseWithJSON(w, respBody, http.StatusOK)
	}
}

func deleteRace(s *mgo.Session) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		session := s.Copy()
		defer session.Close()

		ID := pat.Param(r, "Id")

		c := session.DB(cfg.MongoDatabase).C("races")

		err := c.Remove(bson.M{"Id": ID})
		if err != nil {
			switch err {
			default:
				errorWithJSON(w, "Database error", http.StatusInternalServerError)
				log.Println("Failed delete Race: ", err)
				return
			case mgo.ErrNotFound:
				errorWithJSON(w, "Race not found", http.StatusNotFound)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func appendIfMissing(slice []string, i string) []string {
	for _, ele := range slice {
		if ele == i {
			return slice
		}
	}
	return append(slice, i)
}
