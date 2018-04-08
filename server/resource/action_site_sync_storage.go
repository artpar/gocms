package resource

import (
	"github.com/artpar/rclone/cmd"
	log "github.com/sirupsen/logrus"

	"context"
	"github.com/artpar/api2go"
	"github.com/artpar/rclone/fs/config"
	"github.com/artpar/rclone/fs/sync"
	"github.com/gin-gonic/gin/json"
	"golang.org/x/oauth2"
	"strings"
)

type SyncSiteStorageActionPerformer struct {
	cruds map[string]*DbResource
}

func (d *SyncSiteStorageActionPerformer) Name() string {
	return "site.storage.sync"
}

func (d *SyncSiteStorageActionPerformer) DoAction(request ActionRequest, inFields map[string]interface{}) (api2go.Responder, []ActionResponse, []error) {

	responses := make([]ActionResponse, 0)

	cloudStoreId := inFields["cloud_store_id"].(string)
	tempDirectoryPath := inFields["path"].(string)
	cloudStore, err := d.cruds["cloud_store"].GetCloudStoreByReferenceId(cloudStoreId)
	if err != nil {
		return nil, nil, []error{err}
	}

	oauthTokenId := cloudStore.OAutoTokenId

	token, err := d.cruds["oauth_token"].GetTokenByTokenReferenceId(oauthTokenId)
	oauthConf := &oauth2.Config{}
	if err != nil {
		log.Infof("Failed to get oauth token for store sync: %v", err)
	} else {
		oauthConf, err := d.cruds["oauth_token"].GetOauthDescriptionByTokenReferenceId(oauthTokenId)
		if !token.Valid() {
			ctx := context.Background()
			tokenSource := oauthConf.TokenSource(ctx, token)
			token, err = tokenSource.Token()
			CheckErr(err, "Failed to get new access token")
			if token == nil {
				log.Errorf("we have no token to get the site from storage: %v", cloudStore.ReferenceId)
			} else {
				err = d.cruds["oauth_token"].UpdateAccessTokenByTokenReferenceId(oauthTokenId, token.AccessToken, token.Expiry.Unix())
				CheckErr(err, "failed to update access token")
			}
		}
	}

	jsonToken, err := json.Marshal(token)
	CheckErr(err, "Failed to convert token to json")
	config.FileSet(cloudStore.StoreProvider, "client_id", oauthConf.ClientID)
	config.FileSet(cloudStore.StoreProvider, "type", cloudStore.StoreProvider)
	config.FileSet(cloudStore.StoreProvider, "client_secret", oauthConf.ClientSecret)
	config.FileSet(cloudStore.StoreProvider, "token", string(jsonToken))
	config.FileSet(cloudStore.StoreProvider, "client_scopes", strings.Join(oauthConf.Scopes, ","))
	config.FileSet(cloudStore.StoreProvider, "redirect_url", oauthConf.RedirectURL)

	args := []string{
		cloudStore.RootPath,
		tempDirectoryPath,
	}

	fsrc, fdst := cmd.NewFsSrcDst(args)
	log.Infof("Temp dir for site [%v]/%v ==> %v", cloudStore.Name, cloudStore.RootPath, tempDirectoryPath)
	go cmd.Run(true, true, nil, func() error {
		if fsrc == nil || fdst == nil {
			log.Errorf("Either source or destination is empty")
			return nil
		}
		log.Infof("Starting to copy drive for site base from [%v] to [%v]", fsrc.String(), fdst.String())
		if fsrc == nil || fdst == nil {
			log.Errorf("Source or destination is null")
			return nil
		}
		dir := sync.CopyDir(fdst, fsrc)
		return dir
	})

	restartAttrs := make(map[string]interface{})
	restartAttrs["type"] = "success"
	restartAttrs["message"] = "Cloud storage file upload queued"
	restartAttrs["title"] = "Success"
	actionResponse := NewActionResponse("client.notify", restartAttrs)
	responses = append(responses, actionResponse)

	return nil, responses, nil
}

func NewSyncSiteStorageActionPerformer(cruds map[string]*DbResource) (ActionPerformerInterface, error) {

	handler := SyncSiteStorageActionPerformer{
		cruds: cruds,
	}

	return &handler, nil

}
