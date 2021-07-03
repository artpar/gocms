package resource

import (
	"encoding/base64"
	"fmt"
	"github.com/artpar/go-guerrilla"
	"github.com/daptin/daptin/server/auth"
	"github.com/samedi/caldav-go"
	"github.com/samedi/caldav-go/data"
	"github.com/samedi/caldav-go/errs"
	log "github.com/sirupsen/logrus"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)


type CalDavStorage struct {
	cruds map[string]*DbResource
	Mailer *mailSendActionPerformer
	Username string
	Email string
	UserID  int64
	UserReferenceID string
	GroupID  []auth.GroupPermission
}


func NewCaldavStorage(cruds map[string]*DbResource,certificateManager *CertificateManager)(*CalDavStorage, error){
	d := &guerrilla.Daemon{}
	return &CalDavStorage{
		cruds: cruds,
		Mailer: &mailSendActionPerformer{
			cruds:cruds,
			mailDaemon:d,
			certificateManager:certificateManager,
		},
	}, nil
}


func (cs *CalDavStorage) CalDavHandler() http.Handler{
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {

		writer.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)

		str := strings.SplitN(request.Header.Get("Authorization"), " ", 2)
		if len(str) != 2 {
			http.Error(writer, "Not authorized", 401)
			return
		}

		b, err := base64.StdEncoding.DecodeString(str[1])
		if err != nil {
			http.Error(writer, err.Error(), 401)
			return
		}

		pair := strings.SplitN(string(b), ":", 2)
		if len(pair) != 2 {
			http.Error(writer, "Not authorized", 401)
			return
		}

		pword, err := cs.cruds["user_account"].GetUserPassword(pair[0])
		if err != nil {
			http.Error(writer, "User Not Found!", http.StatusUnauthorized)
		}


		if !BcryptCheckStringHash(pair[1], pword){
			http.Error(writer, "Unauthorized access", http.StatusUnauthorized)
		}

		retrievedUser,err:= cs.cruds["user_account"].GetUserAccountRowByEmail(pair[0])
		if err != nil {
			http.Error(writer, "Unable To Retrieve User", 401)
		}

		userReferenceId := retrievedUser["reference_id"].(string)
		userId := retrievedUser["id"].(int64)


		userGroup, err := cs.cruds[USER_ACCOUNT_TABLE_NAME].GetUserGroupById(USER_ACCOUNT_TABLE_NAME, userId,userReferenceId)
		if err != nil {
			http.Error(writer, "Unable To Retrieve User", 401)
			log.Error("Unable To Retrieve User Group", err)
		}

		stg := new(CalDavStorage)
		stg.cruds    = cs.cruds
		stg.Mailer   = cs.Mailer
		stg.UserID = userId
		stg.Email  = retrievedUser["email"].(string)
		stg.Username = retrievedUser["name"].(string)
		stg.UserReferenceID = userReferenceId
		stg.GroupID = userGroup

		response := caldav.HandleRequestWithStorage(request, stg)
		response.Write(writer)
	})
}


func (cs *CalDavStorage) GetResourcesByList(rpaths []string) ([]data.Resource, error) {
	var results []data.Resource

	for _, rpath := range rpaths {
		resource, found, err := cs.GetShallowResource(rpath)

		if err != nil && err != errs.ResourceNotFoundError {
			return nil, err
		}

		if found {
			results = append(results, *resource)
		}
	}

	return results, nil
}

func (cs *CalDavStorage) GetShallowResource(rpath string) (*data.Resource, bool, error) {
	resources, err := cs.GetResources(rpath, false)

	if err != nil {
		return nil, false, err
	}

	if resources == nil || len(resources) == 0 {
		return nil, false, errs.ResourceNotFoundError
	}

	res := resources[0]
	return &res, true, nil
}

func (cs *CalDavStorage) GetResources(rpath string, withChildren bool) ([]data.Resource, error) {
	var result []data.Resource
	a, err := cs.haveAccess(rpath, "read")

	if err != nil {
		log.Error(err, "failed to get Access [" + rpath + "]")
		return nil, err
	}

	if ! a {
		log.Info("no access to collection [" + rpath + "]")
		return nil, nil
	}

	if !cs.isResourcePresent(rpath){
		log.Info("no resource found for [" + rpath + "]")
		return nil, nil
	}

	res := data.NewResource(rpath, &PGResourceAdapter{db: cs.cruds, resourcePath: rpath, UserID: cs.UserID, UserReferenceId: cs.UserReferenceID})
	result = append(result, res)

	return result, nil
}

func (cs *CalDavStorage) haveAccess(rpath string, perm string) (bool, error) {
	if perm == "admin"{
		return cs.cruds[USER_ACCOUNT_TABLE_NAME].IsAdmin(cs.UserReferenceID), nil

	}

	if !cs.isResourcePresent(rpath){
		return true, nil
	}

	rowPermission := make(map[string]interface{})

	calId, err := cs.cruds["calendar"].GetCalendarId(rpath, cs.UserID)
	if err != nil {
		return false, err
	}

	rowPermission["__type"] = "calendar"
	rowPermission["id"] = calId

	permInst := cs.cruds["calendar"].GetRowPermission(rowPermission)

	if perm == "read"{
		return permInst.CanRead(cs.UserReferenceID, cs.GroupID), nil

	}

	if perm == "write"{
		return permInst.CanCreate(cs.UserReferenceID, cs.GroupID), nil

	}

	return false, nil
}

func isCollection(rpath string) bool {
	if rpath[len(rpath) - 1 : ] == "/" {
		return true
	}
	if rpath[len(rpath) - 3 : ] != "ics" {
		return true
	}
	return false
}

var regex = regexp.MustCompile(`/[A-Za-z0-9-%@\.]*\.ics`)
func getCollection(rpath string) string {
	replace := regex.ReplaceAll([]byte(rpath), []byte("/"))
	return string(replace)
}

func (cs *CalDavStorage) GetResourcesByFilters(rpath string, filters *data.ResourceFilter) ([]data.Resource, error) {
	var result []data.Resource

	res, err := cs.GetResources(rpath, true)
	if err != nil {
		return nil, err
	}
	for _, r := range res {
		if filters == nil || filters.Match(&r) {
			result = append(result, r)
		}
	}

	return result, nil
}

func (cs *CalDavStorage) GetResource(rpath string) (*data.Resource, bool, error) {
	return cs.GetShallowResource(rpath)
}

func (cs *CalDavStorage) CreateResource(rpath, content string) (*data.Resource, error) {
	a, err := cs.haveAccess(rpath, "write")

	if err != nil {
		log.Error(err, "failed to get Access [" + rpath + "]")
		return nil, err
	}
	if ! a {
		log.Info("no access to collection [" + rpath + "]")
		return nil, nil
	}

	c := base64.StdEncoding.EncodeToString([]byte(content))
	err = cs.cruds["calendar"].InsertResource(rpath, c, cs.UserID)

	if err != nil {
		log.Error(err, "failed to insert ", rpath)
		return nil, err
	}

	res := data.NewResource(rpath, &PGResourceAdapter{db: cs.cruds, resourcePath: rpath, UserID: cs.UserID, UserReferenceId: cs.UserReferenceID})
	_ = res.GetPropertyValue("VEVENT", "ATTENDEE")
	title     := res.GetPropertyValue("VEVENT", "SUMMARY")
	start     := res.StartTimeUTC()
	end       := res.EndTimeUTC()
	subject   := "Invitation: " + title + " @ " + start.Weekday().String() + " " + start.Month().String() + " " + strconv.Itoa(start.Day()) + ", " + strconv.Itoa(start.Year()) + " " + strconv.Itoa(start.Hour()) + " - " + strconv.Itoa(end.Hour()) + " (" + start.Location().String() + ") (" + cs.Email + ")"

	actionRequestParameters := make(map[string]interface{})
	actionRequestParameters["to"] = cs.Email
	actionRequestParameters["subject"] = subject
	actionRequestParameters["from"] = "daptin.no-reply"
	actionRequestParameters["body"] = content

	_, _, mailerError := cs.Mailer.DoAction(Outcome{}, actionRequestParameters)
	if mailerError  != nil{
		log.Error("Unable To Send mail", mailerError)
	}

	log.Info("resource created ", rpath)
	return &res, nil
}

func (cs *CalDavStorage) UpdateResource(rpath, content string) (*data.Resource, error) {
	a, err := cs.haveAccess(rpath, "write")
	if err != nil {
		log.Error(err, "failed to get Access [" + rpath + "]")
		return nil, err
	}
	if ! a {
		log.Info("no access to collection [" + rpath + "]")
		return nil, nil
	}
	c := base64.StdEncoding.EncodeToString([]byte(content))
	err = cs.cruds["calendar"].UpdateResource(rpath, c)
	if err != nil {
		log.Error(err, "failed to update ", rpath)
		return nil, err
	}

	res := data.NewResource(rpath, &PGResourceAdapter{db: cs.cruds, resourcePath: rpath,  UserID: cs.UserID, UserReferenceId: cs.UserReferenceID})
	_ = res.GetPropertyValue("VEVENT", "ATTENDEE")
	title     := res.GetPropertyValue("VEVENT", "SUMMARY")
	start     := res.StartTimeUTC()
	end       := res.EndTimeUTC()
	subject   := "Updated invitation: " + title + " @ " + start.Weekday().String() + " " + start.Month().String() + " " + strconv.Itoa(start.Day()) + ", " + strconv.Itoa(start.Year()) + " " + strconv.Itoa(start.Hour()) + " - " + strconv.Itoa(end.Hour()) + " (" + start.Location().String() + ") (" + cs.Email + ")"

	actionRequestParameters := make(map[string]interface{})
	actionRequestParameters["to"] = cs.Email
	actionRequestParameters["subject"] = subject
	actionRequestParameters["from"] = "daptin.no-reply"
	actionRequestParameters["body"] = content


	_, _, mailerError := cs.Mailer.DoAction(Outcome{}, actionRequestParameters)
	if mailerError  != nil{
		log.Error("Unable To Send mail", mailerError)
	}

	log.Info("resource updated ", rpath)
	return &res, nil
}

func (cs *CalDavStorage) DeleteResource(rpath string) error {
	a, err := cs.haveAccess(rpath, "admin")
	if err != nil {
		log.Error(err, "failed to get Access [" + rpath + "]")
		return  err
	}
	if ! a {
		log.Info("no access to collection [" + rpath + "]")
		return nil
	}

	err = cs.cruds["calendar"].DeleteCalendarEvent(cs.UserID, rpath)
	if err != nil {
		log.Info("failed to delete resource ", rpath, " ", err.Error())
		return err
	}

	return nil
}

func (cs *CalDavStorage) isResourcePresent(rpath string) bool {
	rrpath, err := cs.cruds["calendar"].GetRpath(cs.UserID)

	if err != nil {
		return false
	}

	if rrpath == rpath {
		return true
	}

	return false
}

type PGResourceAdapter struct {
	db map[string]*DbResource
	resourcePath string
	UserReferenceId string
	UserID int64

}

func (pa *PGResourceAdapter) CalculateEtag() string {
	if pa.IsCollection() {
		return ""
	}

	return fmt.Sprintf(`"%x%x"`, pa.GetContentSize(), pa.GetModTime().UnixNano())
}

func (pa *PGResourceAdapter) haveAccess(perm string) (bool, error) {
	calId, err := pa.db["calendar"].GetCalendarIdByAccountId("calendar", pa.UserID)
	if err != nil {
		return false, err
	}


	permInst := pa.db["calendar"].GetObjectPermissionById("calendar", calId)
	fmt.Println("permInst", permInst)

	uGroupId := permInst.UserGroupId


	if perm == "read"{
		return permInst.CanRead(pa.UserReferenceId, uGroupId), nil

	}

	if perm == "write"{
		return permInst.CanCreate(pa.UserReferenceId, uGroupId), nil
	}

	if perm == "admin"{
		return pa.db[USER_ACCOUNT_TABLE_NAME].IsAdmin(pa.UserReferenceId), nil
	}

	return false, nil
}

func (pa *PGResourceAdapter) GetContent() string {
	if pa.IsCollection() {
		return ""
	}
	a, err := pa.haveAccess("read")
	if err != nil {
		log.Error(err, "failed to get Access ", pa.resourcePath)
		return ""
	}
	if ! a {
		return ""
	}
	content,err := pa.db["calendar"].GetContent(pa.resourcePath,pa.UserID)
	if err != nil {
		log.Error(err, "failed to fetch content ", pa.resourcePath)
		return ""
	}

	return content
}

func (pa *PGResourceAdapter) GetContentSize() int64 {
	return int64(len(pa.GetContent()))

}

func (pa *PGResourceAdapter) IsCollection() bool {
	return isCollection(pa.resourcePath)
}

func (pa *PGResourceAdapter) GetModTime() time.Time {
	a, err := pa.haveAccess("read")
	if err != nil {
		log.Error(err, "failed to get Access ", pa.resourcePath)
		return time.Unix(0, 0)
	}
	if ! a {
		log.Info("failed to get Access ", pa.resourcePath)
		return time.Unix(0, 0)
	}
	var mod time.Time
	mod, err = pa.db["calendar"].GetModTime(pa.resourcePath, pa.UserID)
	if err != nil {
		log.Error(err, "failed to fetch modTime ", pa.resourcePath)
		return time.Unix(0, 0)
	}
	return mod
}


