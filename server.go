package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"at.ourproject/vfeeg-backend/api"
	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/config"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/eda"
	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/graph"
	"at.ourproject/vfeeg-backend/graph/generated"
	"at.ourproject/vfeeg-backend/logging"
	"at.ourproject/vfeeg-backend/mailtemplate"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/parser"
	"at.ourproject/vfeeg-backend/services"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/httpfs"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var (
	//go:embed migrations/*.sql
	migrations embed.FS
)

func captureOsInterrupt() chan bool {
	quit := make(chan bool)
	go func() {
		c := make(chan os.Signal, 2)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)

		for sig := range c {
			log.Infof("captured %v, stopping and exiting.", sig)

			quit <- true
			close(quit)

			break
		}
	}()
	return quit
}

func InitRouters(db database.Database) *mux.Router {

	r := mux.NewRouter()
	s := r.PathPrefix("/").Subrouter()
	s = api.InitEegRouter(s, db)
	s = api.InitParticipantRouter(s, db)
	s = api.InitMeteringRouter(s, db)
	s = api.InitProcessRouter(s, db)
	s = api.InitApiRouter(s, db)
	s = api.InitHealthRouter(s, database.Ping)
	s.Use(logging.MuxRoute)

	return s
}

// InitGraphQL registers /query on the router.
func InitGraphQL(r *mux.Router) {
	gqlSrv := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{}}))
	gqlSrv.SetRecoverFunc(middleware.GQLRecover)
	gqlSrv.AroundOperations(middleware.GQLAdminCheckLog)
	gqlSrv.AroundOperations(middleware.GQLAccessOperation)
	r.Handle("/query", middleware.GQLProtect(gqlSrv))
}

type driver struct {
	httpfs.PartialDriver
}

func (d *driver) Open(rawURL string) (source.Driver, error) {
	err := d.PartialDriver.Init(http.FS(migrations), "migrations")
	if err != nil {
		return nil, err
	}

	return d, nil
}

func main() {
	// LOG_LEVEL (default info), LOG_FORMAT (default json), SERVICE_VERSION (image build),
	// DEPLOYMENT_ENVIRONMENT (optional); an unknown level or format stops the start. Debug logs import rows and other personal data (261008-ca1).
	logCfg, err := logging.ConfigFromEnv(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logging.Setup(os.Stdout, logCfg)

	var configPath = flag.String("configPath", ".", "Configfile Path")
	flag.Parse()
	config.ReadConfig(*configPath)

	log.SetReportCaller(true)

	source.Register("embed", &driver{})

	db, err := database.GetDB(context.Background())
	if err != nil {
		panic(err)
	}

	// MigrateDB has logged the error
	if err = db.MigrateDB(); err != nil {
		os.Exit(1)
	}

	parser.WarnIgnoredVolumeTemplates()
	StartServer(db)

	err = db.CloseDB()
	if err != nil {
		log.Fatalf("failed to close database")
	}
}

// httpShutdownLimit is the share of the requests in the 30 s the pod has after SIGTERM: they, the
// broker (2.5 s) and the mail worker (services.MailStopLimit + services.MailReportLimit) run in turn.
const httpShutdownLimit = 8 * time.Second

// shutdownHTTP waits up to limit for running requests; what is still running then is cut off.
func shutdownHTTP(srv *http.Server, limit time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Errorf("http server: requests still running after %v are cut off: %v", limit, err)
		_ = srv.Close()
	}
}

func StartServer(db database.Database) {
	log.Info("Start server ...")
	middleware.InitKeycloak()
	broker, err := mqttclient.Broker().Init(mqttclient.NewMqttClient)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	mailtemplate.NotifyDefaultChanges(ctx, db)
	eda.StartMailWorker()
	eda.InitEdaSubscription(ctx)
	energyrequest.Start(ctx, db)
	mqttclient.InitErrorSubscriptions()

	quit := captureOsInterrupt()
	r := InitRouters(db)

	InitGraphQL(r)

	allowedOrigins := handlers.AllowedOrigins([]string{"*"})
	allowedHeaders := handlers.AllowedHeaders(
		[]string{"X-Requested-With",
			"Accept",
			"Accept-Encoding",
			"Accept-Language",
			"Host",
			"authorization",
			"Content-Type",
			"Content-Length",
			"X-Content-Type-Options",
			"Origin",
			"Connection",
			"Referer",
			"User-Agent",
			"Sec-Fetch-Dest",
			"Sec-Fetch-Mode",
			"Sec-Fetch-Site",
			"Cache-Control",
			"tenant",
			"X-tenant"})
	allowedMethods := handlers.AllowedMethods([]string{"GET", "HEAD", "POST", "PUT", "OPTIONS", "DELETE"})
	allowedCredentials := handlers.AllowCredentials()

	go services.StartGRPCServer(quit)

	log.Infof("VFEEG BACKEND Config:  host: %s  port: %d  database:%s  user:%s",
		viper.GetString("database.host"),
		viper.GetInt("database.port"),
		viper.GetString("database.dbname"),
		viper.GetString("database.user"))
	log.Infof("VFEEG BACKEND is going to listen on %s", fmt.Sprintf("127.0.0.1:%d", viper.GetInt("port")))

	srv := &http.Server{
		Handler: handlers.CORS(allowedOrigins, allowedHeaders, allowedMethods, allowedCredentials)(logging.AccessLog(r)),
		Addr:    fmt.Sprintf("0.0.0.0:%d", viper.GetInt("port")),
		// Good practice: enforce timeouts for servers you create!
		WriteTimeout: 180 * time.Second,
		ReadTimeout:  180 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen and serve returned err: %v", err)
		}
	}()

	<-quit
	log.Println("got interruption signal")
	shutdownHTTP(srv, httpShutdownLimit)

	broker.Stop()
	eda.StopMailWorker()
	log.Println("final")

	log.Println("STOP PROGRAM")
}
