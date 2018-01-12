package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go"
	log "github.com/sirupsen/logrus"
)

var (
	bucketName  = flag.String("b", "", "Bucket name to be used for media assets.")
	endPoint    = flag.String("e", "https://play.minio.io:9000", "Choose a custom endpoint.")
	logFilePath = flag.String("l", "./minio-go-media-player.log", "Set a log file.")
	port  = flag.String("p", "8080", "Port to serve.")
)

// The mediaPlayList for the music player on the browser.
// Used for sending the response from listObjects for the player.
type mediaPlayList struct {
	Key string
	URL string
}

// mediaHandlers media handlers.
type mediaHandlers struct {
	minioClient         *minio.Client
	currentPlayingMedia string
}

var supportedAccesEnvs = []string{
	"ACCESS_KEY",
	"AWS_ACCESS_KEY",
	"AWS_ACCESS_KEY_ID",
}

var supportedSecretEnvs = []string{
	"SECRET_KEY",
	"AWS_SECRET_KEY",
	"AWS_SECRET_ACCESS_KEY",
}

// Must get access keys perform a non-exhaustive search through
// environment variables to fetch access keys, fail if its not
// possible.
func mustGetAccessKeys() (accessKey, secretKey string) {
	for _, accessKeyEnv := range supportedAccesEnvs {
		accessKey = os.Getenv(accessKeyEnv)
		if accessKey != "" {
			break
		}
	}
	for _, secretKeyEnv := range supportedSecretEnvs {
		secretKey = os.Getenv(secretKeyEnv)
		if secretKey != "" {
			break
		}
	}
	if accessKey == "" {
		log.Fatalln("Env variable 'ACCESS_KEY, AWS_ACCESS_KEY or AWS_ACCESS_KEY_ID' not set")
	}

	if secretKey == "" {
		log.Fatalln("Env variable 'SECRET_KEY, AWS_SECRET_KEY or AWS_SECRET_ACCESS_KEY' not set")
	}
	return accessKey, secretKey
}

// Finds out whether the url is http(insecure) or https(secure).
func isSecure(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		panic(err)
	}
	return u.Scheme == "https"
}

// Find the Host of the given url.
func findHost(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		panic(err)
	}
	return u.Host
}

func initLogSetting(filePath string) {
	customFormatter := new(log.JSONFormatter)
	customFormatter.TimestampFormat = time.RFC3339Nano
	log.SetFormatter(customFormatter)
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE, 0755)
	if err == nil {
		log.SetOutput(file)
	} else {
		log.SetOutput(os.Stdout)
		log.Info("Failed to log to file, using default stdout")
	}
}

func main() {
	flag.Parse()

	// Bucket name is not optional.
	if *bucketName == "" {
		log.Fatalln("Bucket name cannot be empty.")
	}

	// Fetch access keys if possible or fail.
	accessKey, secretKey := mustGetAccessKeys()

	initLogSetting(*logFilePath)

	// Initialize minio client.
	minioClient, err := minio.New(findHost(*endPoint), accessKey, secretKey, isSecure(*endPoint))
	if err != nil {
		log.Fatalln(err)
	}

	// Initialize media handlers with minio client.
	mediaPlayer := mediaHandlers{
		minioClient: minioClient,
	}

	var enableGin = true
	if enableGin {
		r := gin.Default()
		// Handler to serve the index page.
		r.Static("/player", "web")

		// End point for list object operations.
		// Called when player in the front end is initialized.
		r.GET("/list/v1", mediaPlayer.ListObjectsHandler2)

		// Given point which receives the object name and returns presigned URL in the response.
		r.GET("/getpresign/v1", mediaPlayer.GetPresignedURLHandler2)

		r.GET("/media/playing", mediaPlayer.GetPlayingMedia)
		r.GET("/media/pause", mediaPlayer.PausePlayingMedia)
		r.POST("/media/playing", mediaPlayer.SetPlayingMedia)

		log.Println("Starting media player, please visit your browser at http://localhost:8080/player/index.html")

		r.Run(":" + *port)
	} else {
		// Handler to serve the index page.
		http.Handle("/", http.FileServer(assetFS()))

		// End point for list object operations.
		// Called when player in the front end is initialized.
		http.HandleFunc("/list/v1", mediaPlayer.ListObjectsHandler)

		// Given point which receives the object name and returns presigned URL in the response.
		http.HandleFunc("/getpresign/v1", mediaPlayer.GetPresignedURLHandler)

		log.Println("Starting media player, please visit your browser at http://localhost:8080")

		http.ListenAndServe(":" + *port, nil)
	}
}

func (api mediaHandlers) ListObjectsHandler2(c *gin.Context) {
	// Create a done channel to control 'ListObjects' go routine.
	doneCh := make(chan struct{})

	// Indicate to our routine to exit cleanly upon return.
	defer close(doneCh)

	var playListEntries []mediaPlayList

	// Tracks if first object presigned.
	var firstObjectPresigned bool

	// Set recursive to list all objects.
	var isRecursive = true

	// List all objects from a bucket-name with a matching prefix.
	for objectInfo := range api.minioClient.ListObjects(*bucketName, "", isRecursive, doneCh) {
		if objectInfo.Err != nil {
			c.String(http.StatusInternalServerError, objectInfo.Err.Error())
			return
		}
		objectName := objectInfo.Key // object name.
		playListEntry := mediaPlayList{
			Key: objectName,
		}
		if !firstObjectPresigned {
			// Generating presigned url for the first object in the list.
			// presigned URL will be generated on the fly for the
			// other objects when they are played.
			expirySecs := 24 * 7 * time.Hour // 7 days.
			presignedURL, err := api.minioClient.PresignedGetObject(*bucketName, objectName, expirySecs, nil)
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			playListEntry.URL = presignedURL.String()
		}
		playListEntries = append(playListEntries, playListEntry)
	}
	playListEntriesJSON, err := json.Marshal(playListEntries)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	// Successfully wrote play list in json.
	c.JSON(http.StatusOK, string(playListEntriesJSON))
}

// ListObjectsHandler - handler for ListsObjects from the Object Storage server and bucket configured above.
func (api mediaHandlers) ListObjectsHandler(w http.ResponseWriter, r *http.Request) {
	// Create a done channel to control 'ListObjects' go routine.
	doneCh := make(chan struct{})

	// Indicate to our routine to exit cleanly upon return.
	defer close(doneCh)

	var playListEntries []mediaPlayList

	// Tracks if first object presigned.
	var firstObjectPresigned bool

	// Set recursive to list all objects.
	var isRecursive = true

	// List all objects from a bucket-name with a matching prefix.
	for objectInfo := range api.minioClient.ListObjects(*bucketName, "", isRecursive, doneCh) {
		if objectInfo.Err != nil {
			http.Error(w, objectInfo.Err.Error(), http.StatusInternalServerError)
			return
		}
		objectName := objectInfo.Key // object name.
		playListEntry := mediaPlayList{
			Key: objectName,
		}
		if !firstObjectPresigned {
			// Generating presigned url for the first object in the list.
			// presigned URL will be generated on the fly for the
			// other objects when they are played.
			expirySecs := 1000 * time.Second // 1000 seconds.
			presignedURL, err := api.minioClient.PresignedGetObject(*bucketName, objectName, expirySecs, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			playListEntry.URL = presignedURL.String()
		}
		playListEntries = append(playListEntries, playListEntry)
	}
	playListEntriesJSON, err := json.Marshal(playListEntries)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Successfully wrote play list in json.
	w.Write(playListEntriesJSON)
}

func (api *mediaHandlers) GetPlayingMedia(c *gin.Context) {
	ts := c.Query("ts")
	appId := c.Query("appId")
	log.Println("GetPlayingMedia: ", api.currentPlayingMedia, ",", ts, ",", appId)
	c.JSON(http.StatusOK, gin.H{
		"playing": api.currentPlayingMedia,
		"ts":      ts,
	})
	return
}

func (api *mediaHandlers) PausePlayingMedia(c *gin.Context) {
	api.currentPlayingMedia = ""
	c.String(http.StatusOK, "")
	return
}

func (api *mediaHandlers) SetPlayingMedia(c *gin.Context) {
	objectName := c.PostForm("objname")
	if objectName != "" {
		api.currentPlayingMedia = objectName
		log.Println("SetPlayingMedia: ", api.currentPlayingMedia)
	}
	c.String(http.StatusOK, objectName)
	return
}

// GetPresignedURLHandler - generates presigned access URL for an object.
func (api mediaHandlers) GetPresignedURLHandler2(c *gin.Context) {
	// The object for which the presigned URL has to be generated is sent as a query
	// parameter from the client.
	objectName := c.Query("objname")
	if objectName == "" {
		c.String(http.StatusBadRequest, "No object name set, invalid request.")
		return
	}
	presignedURL, err := api.minioClient.PresignedGetObject(*bucketName, objectName, 24*7*time.Hour, nil)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.String(http.StatusOK, presignedURL.String())
}

// GetPresignedURLHandler - generates presigned access URL for an object.
func (api mediaHandlers) GetPresignedURLHandler(w http.ResponseWriter, r *http.Request) {
	// The object for which the presigned URL has to be generated is sent as a query
	// parameter from the client.
	objectName := r.URL.Query().Get("objname")
	if objectName == "" {
		http.Error(w, "No object name set, invalid request.", http.StatusBadRequest)
		return
	}
	presignedURL, err := api.minioClient.PresignedGetObject(*bucketName, objectName, 24*7*time.Hour, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte(presignedURL.String()))
}
