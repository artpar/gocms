package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/buraksezer/olric"
	olricConfig "github.com/buraksezer/olric/config"
	"github.com/daptin/daptin/server/auth"
	server2 "github.com/fclairamb/ftpserver/server"
	"io/ioutil"
	"net/url"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/GeertJohan/go.rice"
	"github.com/artpar/go-guerrilla"
	imapServer "github.com/artpar/go-imap/server"
	"github.com/daptin/daptin/server"
	"github.com/daptin/daptin/server/resource"
	"github.com/daptin/daptin/server/statementbuilder"
	"github.com/gin-gonic/gin"
	"github.com/gocraft/health"
	"github.com/jamiealquiza/envy"
	"github.com/sadlil/go-trigger"
	log "github.com/sirupsen/logrus"

	//"io"
	"net/http"
	"os"
	"syscall"
)

// Save the stream as a global variable
var stream = health.NewStream()

func init() {

	//logFileLocation, ok := os.LookupEnv("DAPTIN_LOG_LOCATION")
	//if !ok || logFileLocation == "" {
	//	logFileLocation = "daptin.log"
	//}
	//f, e := os.OpenFile(logFileLocation, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	//if e != nil {
	//	log.Errorf("Failed to open logfile %v", e)
	//}
	//
	//mwriter := io.MultiWriter(f, os.Stdout)
	//
	//log.SetOutput(mwriter)
}

// Following variables will be statically linked at the time of compiling

// GitCommit holds short commit hash of source tree
var GitCommit string

// GitBranch holds current branch name the code is built off
var GitBranch string

// GitState shows whether there are uncommitted changes
var GitState string

// GitSummary holds output of git describe --tags --dirty --always
var GitSummary string

// BuildDate holds RFC3339 formatted UTC date (build time)
var BuildDate string

// Version holds contents of ./VERSION file, if exists, or the value passed via the -version option
var Version string

func printVersion() {
	fmt.Printf(`
   GitCommit: %s
   GitBranch: %s
    GitState: %s
  GitSummary: %s
   BuildDate: %s
     Version: %s
	`, GitCommit, GitBranch, GitState, GitSummary, BuildDate, Version)
}

func main() {

	restartLock := sync.Mutex{}
	//eventEmitter := &emitter.Emitter{}

	var dbType = flag.String("db_type", "sqlite3", "Database to use: sqlite3/mysql/postgres")
	var localStoragePath = flag.String("local_storage_path", "./storage", "Path where blob column assets will be stored, set to ; to disable")
	var connectionString = flag.String("db_connection_string", "daptin.db", "\n\tSQLite: test.db\n"+
		"\tMySql: <username>:<password>@tcp(<hostname>:<port>)/<db_name>\n"+
		"\tPostgres: host=<hostname> port=<port> user=<username> password=<password> dbname=<db_name> sslmode=enable/disable")

	var webDashboardSource = flag.String("dashboard", "daptinweb", "path to dist folder for daptin web dashboard")
	//var assetsSource = flag.String("assets", "assets", "path to folder for assets")
	var port_variable = flag.String("port_variable", "DAPTIN_PORT", "ENV port variable name to look for port")
	var database_url_variable = flag.String("database_url_variable", "DAPTIN_DB_CONNECTION_STRING", "ENV port variable name to look for port")
	var port = flag.String("port", ":6336", "daptin port")
	var httpsPort = flag.String("https_port", ":6443", "daptin https port")
	var runtimeMode = flag.String("runtime", "release", "Runtime for Gin: profile, debug, test, release")
	var logLevel = flag.String("log_level", "info", "log level : debug, trace, info, warn, error, fatal")
	var profileDumpPath = flag.String("profile_dump_path", "./", "location for dumping cpu/heap data in profile mode")
	var profileDumpPeriod = flag.Int("profile_dump_period", 5, "time period in minutes for triggering profile dump")

	envy.Parse("DAPTIN") // looks for DAPTIN_PORT, DAPTIN_DASHBOARD, DAPTIN_DB_TYPE, DAPTIN_RUNTIME
	flag.Parse()

	printVersion()

	log.Infof("Runtime is %s", *runtimeMode)
	logLevelParsed, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Errorf("invalid log level: %s, setting to info", *logLevel)
		logLevelParsed = log.InfoLevel
	}
	log.SetLevel(logLevelParsed)
	profileDumpCount := 0
	if *runtimeMode == "profile" {
		gin.SetMode("release")
		log.Infof("Dumping CPU/Heap Profile at %s every %v Minutes", *profileDumpPath, *profileDumpPeriod)

		cpuprofile := fmt.Sprintf("%sdaptin_cpu_profile_%v.prof", *profileDumpPath, profileDumpCount)
		heapprofile := fmt.Sprintf("%sdaptin_heap_profile_%v.prof", *profileDumpPath, profileDumpCount)
		cpuFile, err1 := os.Create(cpuprofile)
		heapFile, err2 := os.Create(heapprofile)
		if err1 != nil || err2 != nil {
			log.Errorf("Failed to create file for profile dump: %v - %v", err1, err2)
		} else {
			var err error
			err = pprof.StartCPUProfile(cpuFile)
			auth.CheckErr(err, "Failed to start CPU profile: %v", err)
			err = pprof.WriteHeapProfile(heapFile)
			auth.CheckErr(err, "Failed to start HEAP profile: %v", err)
		}
	} else {
		gin.SetMode(*runtimeMode)
	}

	stream.AddSink(&health.WriterSink{
		Writer: os.Stdout,
	})

	boxRoot1, err := rice.FindBox("daptinweb")

	var boxRoot http.FileSystem
	if err != nil || (webDashboardSource != nil && *webDashboardSource != "daptinweb") {
		log.Errorf("Dashboard not loading from default path: %v == %v", err, boxRoot1)
		log.Printf("Try loading web dashboard from: %v", *webDashboardSource)
		boxRoot = http.Dir(*webDashboardSource)
	} else {
		boxRoot = boxRoot1.HTTPBox()
	}

	if database_url_variable != nil && *database_url_variable != "DAPTIN_DB_CONNECTION_STRING" {

		databaseUrlValue, ok := os.LookupEnv(*database_url_variable)
		if ok && len(databaseUrlValue) > 0 {
			if strings.Index(databaseUrlValue, "://") > -1 {
				log.Printf("Connection URL found for database in env variable [%v]", *database_url_variable)
				databaseUrlParsed, err := url.Parse(databaseUrlValue)

				if err != nil {
					log.Printf("Unable to parse database variable value as url, passing it as it is")
					connectionString = &databaseUrlValue
				} else {
					password, _ := databaseUrlParsed.User.Password()
					databaseName := strings.Split(databaseUrlParsed.Path, "/")[1]
					switch databaseUrlParsed.Scheme {
					case "postgresql":
						fallthrough
					case "postgres":
						x := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
							databaseUrlParsed.Hostname(), databaseUrlParsed.Port(), databaseUrlParsed.User.Username(), password, databaseName)
						connectionString = &x
					case "mysql":
						x := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s",
							databaseUrlParsed.User.Username(), password, databaseUrlParsed.Hostname(), databaseUrlParsed.Port(), databaseName)
						connectionString = &x
					default:
						connectionString = &databaseUrlValue
					}
				}
			} else {
				connectionString = &databaseUrlValue
			}

			if *dbType == "" {
				if strings.Index(*connectionString, "dbname=") > -1 {
					*dbType = "postgres"
				} else if strings.Index(*connectionString, "@tcp") > -1 {
					*dbType = "mysql"
				}
			}
		}

	}

	statementbuilder.InitialiseStatementBuilder(*dbType)
	auth.PrepareAuthQueries()
	log.Printf("Database connection using: [%v] [%v]", *dbType, *connectionString)

	db, err := server.GetDbConnection(*dbType, *connectionString)
	if err != nil {
		panic(err)
	}
	tx := db.MustBegin()
	_ = tx.Rollback()
	log.Printf("Connection acquired from database [%s]", *dbType)

	var hostSwitch server.HostSwitch
	var mailDaemon *guerrilla.Daemon
	var taskScheduler resource.TaskScheduler
	var certManager *resource.CertificateManager
	var configStore *resource.ConfigStore
	var ftpServer *server2.FtpServer
	var imapServerInstance *imapServer.Server
	var olricDb *olric.Olric

	if localStoragePath != nil && *localStoragePath != "" {
		if _, err := os.Stat(*localStoragePath); err == os.ErrNotExist {
			_ = os.Mkdir(*localStoragePath, 0644)
		}
	}

	olricConfig1 := olricConfig.New("wan")
	olricConfig1.LogLevel = "ERROR"
	olricConfig1.LogVerbosity = 1
	olricConfig1.LogOutput = os.Stderr

	olricDb, err = olric.New(olricConfig1)
	if err != nil {
		log.Errorf("Failed to create olric cache: %v", err)
	}

	go func() {
		err = olricDb.Start()
		resource.CheckErr(err, "failed to start cache server")
	}()

	hostSwitch, mailDaemon, taskScheduler, configStore, certManager,
		ftpServer, imapServerInstance, olricDb = server.Main(boxRoot, db, *localStoragePath, olricDb)
	rhs := RestartHandlerServer{
		HostSwitch: &hostSwitch,
	}

	if *runtimeMode == "profile" {

		go func() {

			for {
				time.Sleep(time.Duration(*profileDumpPeriod) * time.Minute)
				log.Infof("Dumping cpu and heap profile at %s", *profileDumpPath)
				profileDumpCount += 1
				pprof.StopCPUProfile()

				cpuprofile := fmt.Sprintf("%sdaptin_profile_cpu.%v", *profileDumpPath, profileDumpCount)
				heapprofile := fmt.Sprintf("%sdaptin_profile_heap.%v", *profileDumpPath, profileDumpCount)

				cpuFile, err := os.Create(cpuprofile)
				heapFile, err := os.Create(heapprofile)
				if err != nil {
					log.Errorf("Failed to create file [%v] for profile dump: %v", cpuprofile, err)
				} else {
					err = pprof.StartCPUProfile(cpuFile)
					auth.CheckErr(err, "Failed to start CPU profile: %v", err)
					err = pprof.WriteHeapProfile(heapFile)
					auth.CheckErr(err, "Failed to start HEAP profile: %v", err)
				}
			}

		}()

	}

	err = trigger.On("restart", func() {
		log.Printf("Trigger restart")
		restartLock.Lock()
		defer restartLock.Unlock()

		startTime := time.Now()

		log.Printf("Close down services and db connection")
		taskScheduler.StopTasks()
		if ftpServer != nil {
			ftpServer.Stop()
		}

		if mailDaemon != nil {
			mailDaemon.Shutdown()
		}

		if imapServerInstance != nil {
			err = imapServerInstance.Close()
			if err != nil {
				log.Printf("Failed to close imap server connections: %v", err)
			}
		}
		log.Printf("All connections closed")
		log.Printf("Create new connections")
		db1, err := server.GetDbConnection(*dbType, *connectionString)
		auth.CheckErr(err, "Failed to create new db connection")
		if err != nil {
			return
		}

		hostSwitch, mailDaemon, taskScheduler, configStore, certManager,
			ftpServer, imapServerInstance, olricDb = server.Main(boxRoot, db1, *localStoragePath, olricDb)
		rhs.HostSwitch = &hostSwitch
		err = db.Close()
		auth.CheckErr(err, "Failed to close old db connection")
		log.Printf("Restart complete, took %f seconds", float64(time.Now().UnixNano()-startTime.UnixNano())/float64(1000000000))

	})

	resource.CheckErr(err, "Error while adding restart trigger function")

	portValue := *port
	if strings.Index(portValue, ".") > -1 {
		// port has ip and nothing to do
	} else if portValue[0] != ':' {
		// port is missing :
		portValue = ":" + portValue
	}

	if port_variable != nil && *port_variable != "DAPTIN_PORT" {
		portVarString := *port_variable

		portVarStringValue, ok := os.LookupEnv(portVarString)
		log.Infof("Looking up variable [%s] for port: %v", portVarString, portVarStringValue)
		if ok && len(portVarStringValue) > 0 {
			if portVarStringValue[0] != ':' {
				log.Infof("Port value picked from  env is missing colon: %v", portVarStringValue)
				portVarStringValue = ":" + portVarStringValue
			}
			portValue = portVarStringValue
		}
	}

	log.Printf("[%v] Listening at port: %v", syscall.Getpid(), portValue)

	enableHttps, err := configStore.GetConfigValueFor("enable_https", "backend")
	if err != nil {
		enableHttps = "false"
		_ = configStore.SetConfigValueFor("enable_https", enableHttps, "backend")
	}

	hostname, err := configStore.GetConfigValueFor("hostname", "backend")

	_, certBytes, privateBytes, _, rootCertBytes, err := certManager.GetTLSConfig(hostname, true)

	if err == nil && enableHttps == "true" {
		go func() {

			certTempDir := os.TempDir()
			certFile := certTempDir + "/" + hostname + ".crt"
			keyFile := certTempDir + "/" + hostname + ".key"
			log.Printf("Temp dir for certificates: %v", certTempDir)
			certPem := []byte(string(certBytes) + "\n" + string(rootCertBytes))
			err = ioutil.WriteFile(certFile, certPem, 0600)
			resource.CheckErr(err, "Failed to write cert file")
			keyPem := privateBytes
			err = ioutil.WriteFile(keyFile, keyPem, 0600)
			resource.CheckErr(err, "Failed to write private key file")

			cert, err := tls.X509KeyPair(certPem, keyPem)
			if err != nil {
				log.Errorf("Failed to load cert for TLS [%v]", hostname)
				return
			}

			tlsServer := &http.Server{Addr: *httpsPort, Handler: &rhs}
			tlsServer.TLSConfig.Certificates = []tls.Certificate{
				{
					Certificate: [][]byte{certBytes},
					PrivateKey:  cert,
				},
			}

			err1 := tlsServer.ListenAndServeTLS("", "")
			if err1 != nil {
				log.Errorf("Failed to start TLS server: %v", err1)
			}
		}()
	} else {
		log.Errorf("Not starting HTTPS server: %v: %v", hostname, err)
	}

	log.Printf("Listening at: [%v]", portValue)
	err = http.ListenAndServe(portValue, &rhs)
	if err != nil {
		panic(err)
	}

	log.Printf("Why quit now ?")
}

//eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJlbWFpbCI6InRoZXRlY2h0ZWtrZXJAZ21haWwuY29tIiwiZXhwIjoxNjI0MTg4NDIwLCJpYXQiOiIyMDIxLTA2LTE3VDExOjI3OjAwLjUxMDU1ODMwM1oiLCJpc3MiOiJkYXB0aW4tZGEzYmIwIiwianRpIjoiYWIyNzQwMTgtYjZkYi00ZjY4LWFkNDktNjgyMDJkY2U5NDExIiwibmFtZSI6IlRvYnkiLCJuYmYiOjE2MjM5MjkyMjAsInN1YiI6InRoZXRlY2h0ZWtrZXJAZ21haWwuY29tIn0.6hmw3Iz3oaArIO93VXNyQDvy6NA9rNZFlu0g_mGR6Oc
// RestartHandlerServer helps in switching the new router with old router with restart is triggered
type RestartHandlerServer struct {
	HostSwitch *server.HostSwitch
}

func (rhs *RestartHandlerServer) ServeHTTP(rew http.ResponseWriter, req *http.Request) {
	rhs.HostSwitch.ServeHTTP(rew, req)
}
