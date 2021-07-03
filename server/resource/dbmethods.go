package resource

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/araddon/dateparse"
	"github.com/artpar/api2go"
	uuid "github.com/artpar/go.uuid"
	"github.com/buraksezer/olric"
	"github.com/daptin/daptin/server/auth"
	fieldtypes "github.com/daptin/daptin/server/columntypes"
	"github.com/daptin/daptin/server/statementbuilder"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

const DATE_LAYOUT = "2006-01-02 15:04:05"

// IsUserActionAllowed Checks if a user identified by userReferenceId and belonging to userGroups is allowed to invoke an action `actionName` on type `typeName`
// Called before invoking an action from the /action/** api
// Checks EXECUTE on both the type and action for this user
// The permissions can come from different groups
func (dr *DbResource) IsUserActionAllowed(userReferenceId string, userGroups []auth.GroupPermission, typeName string, actionName string) bool {

	permission := dr.GetObjectPermissionByWhereClause("world", "table_name", typeName)

	actionPermission := dr.GetObjectPermissionByWhereClause("action", "action_name", actionName)

	canExecuteOnType := permission.CanExecute(userReferenceId, userGroups)
	canExecuteAction := actionPermission.CanExecute(userReferenceId, userGroups)

	return canExecuteOnType && canExecuteAction

}

// GetActionByName Gets an Action instance by `typeName` and `actionName`
// Check Action instance for usage
func (dr *DbResource) GetActionByName(typeName string, actionName string) (Action, error) {
	var a ActionRow

	var action Action

	sql, args, err := statementbuilder.Squirrel.Select(
		goqu.I("a.action_name").As("name"),
		goqu.I("w.table_name").As("ontype"),
		goqu.I("a.label").As("label"),
		goqu.I("action_schema").As("action_schema"),
		goqu.I("a.reference_id").As("referenceid"),
	).From(goqu.T("action").As("a")).
		Join(
			goqu.T("world").As("w"),
			goqu.On(goqu.Ex{
				"w.id": goqu.I("a.world_id"),
			}),
		).Where(goqu.Ex{"w.table_name": typeName}).Where(goqu.Ex{"a.action_name": actionName}).Limit(1).ToSQL()

	if err != nil {
		return action, err
	}

	stmt, err := dr.connection.Preparex(sql)
	if err != nil {
		log.Errorf("[72] failed to prepare statment: %v", err)
		return action, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	err = stmt.QueryRowx(args...).StructScan(&a)

	if err != nil {
		log.Errorf("sql: %v", sql)
		log.Errorf("Failed to scan action 66: %v", err)
		return action, err
	}

	err = json.Unmarshal([]byte(a.ActionSchema), &action)
	CheckErr(err, "failed to unmarshal infields")

	action.Name = a.Name
	action.Label = a.Name
	action.ReferenceId = a.ReferenceId
	action.OnType = a.OnType

	return action, nil
}

// GetActionsByType Gets the list of all actions defined on type `typeName`
// Returns list of `Action`
func (dr *DbResource) GetActionsByType(typeName string) ([]Action, error) {
	action := make([]Action, 0)

	sql, args, err := statementbuilder.Squirrel.Select(
		goqu.I("a.action_name").As("name"),
		goqu.I("w.table_name").As("ontype"),
		goqu.I("a.label"),
		goqu.I("action_schema"),
		goqu.I("instance_optional"),
		goqu.I("a.reference_id").As("referenceid"),
	).From(goqu.T("action").As("a")).Join(goqu.T("world").As("w"), goqu.On(goqu.Ex{
		"w.id": goqu.I("a.world_id"),
	})).Where(goqu.Ex{
		"w.table_name": typeName,
	}).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt, err := dr.connection.Preparex(sql)
	if err != nil {
		log.Errorf("[124] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	rows, err := stmt.Queryx(args...)
	if err != nil {
		log.Errorf("[126] Failed to scan action: %v", err)
		return action, err
	}
	defer func(rows *sqlx.Rows) {
		err := rows.Close()
		if err != nil {
			log.Errorf("[132] failed to close row after value scan")
		}
	}(rows)

	for rows.Next() {

		var act Action
		var a ActionRow
		err := rows.StructScan(&a)
		CheckErr(err, "Failed to struct scan action row")

		if len(a.Label) < 1 {
			continue
		}
		err = json.Unmarshal([]byte(a.ActionSchema), &act)
		CheckErr(err, "failed to unmarshal infields")

		act.Name = a.Name
		act.Label = a.Label
		act.ReferenceId = a.ReferenceId
		act.OnType = a.OnType
		act.InstanceOptional = a.InstanceOptional

		action = append(action, act)

	}

	return action, nil
}

// GetActionPermissionByName Gets permission of an action by typeId and actionName
// Loads the owner, usergroup and guest permission of the action from the database
// Return a PermissionInstance
// Special utility function for actions, for other objects use GetObjectPermissionByReferenceId
func (dr *DbResource) GetActionPermissionByName(worldId int64, actionName string) (PermissionInstance, error) {

	refId, err := dr.GetReferenceIdByWhereClause("action", goqu.Ex{"action_name": actionName}, goqu.Ex{"world_id": worldId})
	if err != nil {
		return PermissionInstance{}, err
	}

	if refId == nil || len(refId) < 1 {
		return PermissionInstance{}, errors.New(fmt.Sprintf("Failed to find action [%v] on [%v]", actionName, worldId))
	}
	permissions := dr.GetObjectPermissionByReferenceId("action", refId[0])

	return permissions, nil
}

// GetObjectPermissionByReferenceId Gets permission of an Object by typeName and string referenceId
// Loads the owner, usergroup and guest permission of the action from the database
// Return a PermissionInstance
// Return a NoPermissionToAnyone if no such object exist
func (dr *DbResource) GetObjectPermissionByReferenceId(objectType string, referenceId string) PermissionInstance {

	var selectQuery string
	var queryParameters []interface{}
	var err error
	var perm PermissionInstance
	if objectType == "usergroup" {
		selectQuery, queryParameters, err = statementbuilder.Squirrel.
			Select("permission", "id").
			From(objectType).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()
	} else {
		selectQuery, queryParameters, err = statementbuilder.Squirrel.
			Select(USER_ACCOUNT_ID_COLUMN, "permission", "id").
			From(objectType).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()

	}

	if err != nil {
		log.Errorf("Failed to create sql: %v", err)
		return perm
	}

	stmt, err := dr.connection.Preparex(selectQuery)
	if err != nil {
		log.Errorf("[219] failed to prepare statment: %v", err)
		return perm
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	resultObject := make(map[string]interface{})
	err = stmt.QueryRowx(queryParameters...).MapScan(resultObject)
	if err != nil {
		log.Errorf("Failed to scan permission 1 [%v]: %v", referenceId, err)
	}
	//log.Printf("permi map: %v", resultObject)
	if resultObject[USER_ACCOUNT_ID_COLUMN] != nil {

		user, err := dr.GetIdToReferenceId(USER_ACCOUNT_TABLE_NAME, resultObject[USER_ACCOUNT_ID_COLUMN].(int64))
		if err == nil {
			perm.UserId = user
		}

	}

	i, ok := resultObject["id"].(int64)
	if !ok {
		return perm
	}
	perm.UserGroupId = dr.GetObjectGroupsByObjectId(objectType, i)

	perm.Permission = auth.AuthPermission(resultObject["permission"].(int64))
	if err != nil {
		log.Errorf("Failed to scan permission 2: %v", err)
	}

	//log.Printf("PermissionInstance for [%v]: %v", typeName, perm)
	return perm
}


// Get permission of an Object by typeName and string referenceId
// Loads the owner, usergroup and guest permission of the action from the database
// Return a PermissionInstance
// Return a NoPermissionToAnyone if no such object exist
func (dr *DbResource) GetObjectPermissionById(objectType string, id int64) PermissionInstance {

	var selectQuery string
	var queryParameters []interface{}
	var err error
	var perm PermissionInstance
	if objectType == "usergroup" {
		selectQuery, queryParameters, err = statementbuilder.Squirrel.
			Select("permission", "id").
			From(objectType).Where(goqu.Ex{"id": id}).
			ToSQL()
	} else {
		selectQuery, queryParameters, err = statementbuilder.Squirrel.
			Select(USER_ACCOUNT_ID_COLUMN, "permission", "id").
			From(objectType).Where(goqu.Ex{"id": id}).
			ToSQL()

	}

	if err != nil {
		log.Errorf("Failed to create sql: %v", err)
		return perm
	}

	stmt, err := dr.connection.Preparex(selectQuery)
	if err != nil {
		log.Errorf("[289] failed to prepare statment: %v", err)
		return perm
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	resultObject := make(map[string]interface{})
	err = stmt.QueryRowx(queryParameters...).MapScan(resultObject)
	if err != nil {
		log.Errorf("Failed to scan permission 3 [%v]: %v", id, err)
	}
	//log.Printf("permi map: %v", resultObject)
	if resultObject[USER_ACCOUNT_ID_COLUMN] != nil {

		user, err := dr.GetIdToReferenceId(USER_ACCOUNT_TABLE_NAME, resultObject["user_account_id"].(int64))
		if err == nil {
			perm.UserId = user
		}
	}

	perm.UserGroupId = dr.GetObjectGroupsByObjectId(objectType, resultObject["id"].(int64))

	perm.Permission = auth.AuthPermission(resultObject["permission"].(int64))
	if err != nil {
		log.Errorf("Failed to scan permission 2: %v", err)
	}

	//log.Printf("PermissionInstance for [%v]: %v", typeName, perm)
	return perm
}

var OlricCache *olric.DMap

// GetObjectPermissionByWhereClause Gets permission of an Object by typeName and string referenceId with a simple where clause colName = colValue
// Use carefully
// Loads the owner, usergroup and guest permission of the action from the database
// Return a PermissionInstance
// Return a NoPermissionToAnyone if no such object exist
func (dr *DbResource) GetObjectPermissionByWhereClause(objectType string, colName string, colValue string) PermissionInstance {
	if OlricCache == nil {
		OlricCache, _ = dr.OlricDb.NewDMap("default-cache")
	}

	cacheKey := ""
	if OlricCache != nil {
		cacheKey = fmt.Sprintf("%s_%s_%s", objectType, colName, colValue)
		cachedPermission, err := OlricCache.Get(cacheKey)
		if cachedPermission != nil && err == nil {
			return cachedPermission.(PermissionInstance)
		}
	}

	var perm PermissionInstance
	s, q, err := statementbuilder.Squirrel.Select(USER_ACCOUNT_ID_COLUMN, "permission", "id").From(objectType).Where(goqu.Ex{colName: colValue}).ToSQL()
	if err != nil {
		log.Errorf("Failed to create sql: %v", err)
		return perm
	}

	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[355] failed to prepare statment: %v", err)
		return perm
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	m := make(map[string]interface{})
	err = stmt.QueryRowx(q...).MapScan(m)

	if err != nil {

		log.Errorf("Failed to scan permission: %v", err)
		return perm
	}

	//log.Printf("permi map: %v", m)
	if m["user_account_id"] != nil {

		user, err := dr.GetIdToReferenceId(USER_ACCOUNT_TABLE_NAME, m[USER_ACCOUNT_ID_COLUMN].(int64))
		if err == nil {
			perm.UserId = user
		}

	}

	perm.UserGroupId = dr.GetObjectGroupsByObjectId(objectType, m["id"].(int64))

	perm.Permission = auth.AuthPermission(m["permission"].(int64))

	//log.Printf("PermissionInstance for [%v]: %v", typeName, perm)

	if OlricCache != nil {
		_ = OlricCache.PutIfEx(cacheKey, perm, 10*time.Second, olric.IfNotFound)
	}
	return perm
}

// GetObjectUserGroupsByWhere Get list of group permissions for objects of typeName where colName=colValue
// Utility method which makes a join query to load a lot of permissions quickly
// Used by GetRowPermission
func (dr *DbResource) GetObjectUserGroupsByWhere(objectType string, colName string, colValue interface{}) []auth.GroupPermission {

	//if OlricCache == nil {
	//	OlricCache, _ = dr.OlricDb.NewDMap("default-cache")
	//}
	//
	//cacheKey := ""
	//if OlricCache != nil {
	//	cacheKey = fmt.Sprintf("groups-%s_%s_%s", objectType, colName, colValue)
	//	cachedPermission, err := OlricCache.Get(cacheKey)
	//	if cachedPermission != nil && err == nil {
	//		return cachedPermission.([]auth.GroupPermission)
	//	}
	//}

	s := make([]auth.GroupPermission, 0)

	rel := api2go.TableRelation{}
	rel.Subject = objectType
	rel.SubjectName = objectType + "_id"
	rel.Object = "usergroup"
	rel.ObjectName = "usergroup_id"
	rel.Relation = "has_many_and_belongs_to_many"

	//log.Printf("Join string: %v: ", rel.GetJoinString())

	sql, args, err := statementbuilder.Squirrel.Select(
		goqu.I("usergroup_id.reference_id").As("groupreferenceid"),
		goqu.I(rel.GetJoinTableName()+".reference_id").As("relationreferenceid"),
		goqu.I(rel.GetJoinTableName()+".permission").As("permission"),
	).From(goqu.T(rel.GetSubject())).
		// rel.GetJoinString()
		Join(goqu.T(rel.GetJoinTableName()).As(rel.GetJoinTableName()),
			goqu.On(goqu.Ex{
				fmt.Sprintf("%v.%v", rel.GetJoinTableName(), rel.GetSubjectName()): goqu.I(fmt.Sprintf("%v.%v", rel.GetSubject(), "id")),
			})).
		Join(goqu.T(rel.GetObject()).As(rel.GetObjectName()),
			goqu.On(goqu.Ex{
				fmt.Sprintf("%v.%v", rel.GetJoinTableName(), rel.GetObjectName()): goqu.I(fmt.Sprintf("%v.%v", rel.GetObjectName(), "id")),
			})).
		Where(goqu.Ex{
			fmt.Sprintf("%s.%s", rel.Subject, colName): colValue,
		}).ToSQL()
	if err != nil {
		log.Errorf("Failed to create permission select query: %v", err)
		return s
	}

	stmt, err := dr.connection.Preparex(sql)
	if err != nil {
		log.Errorf("[436] failed to prepare statment: %v", err)
		return nil
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	res, err := stmt.Queryx(args...)
	//log.Printf("Group select sql: %v", sql)
	if err != nil {

		log.Errorf("Failed to get object groups by where clause: %v", err)
		log.Errorf("Query: %s == [%v]", sql, args)
		return s
	}
	defer res.Close()

	for res.Next() {
		var g auth.GroupPermission
		err = res.StructScan(&g)
		if err != nil {
			log.Errorf("Failed to scan group permission 1: %v", err)
		}
		s = append(s, g)
	}

	//if OlricCache != nil {
	//	_ = OlricCache.PutIfEx(cacheKey, s, 10*time.Second, olric.IfNotFound)
	//}

	return s

}
func (dr *DbResource) GetObjectGroupsByObjectId(objType string, objectId int64) []auth.GroupPermission {
	s := make([]auth.GroupPermission, 0)

	refId, err := dr.GetIdToReferenceId(objType, objectId)

	if objType == "usergroup" {

		if err != nil {
			log.Printf("Failed to get id to reference id [%v][%v] == %v", objType, objectId, err)
			return s
		}
		s = append(s, auth.GroupPermission{
			GroupReferenceId:    refId,
			ObjectReferenceId:   refId,
			RelationReferenceId: refId,
			Permission:          auth.AuthPermission(dr.Cruds["usergroup"].model.GetDefaultPermission()),
		})
		return s
	}

	sql, args, err := statementbuilder.Squirrel.Select(
		goqu.I("ug.reference_id").As("groupreferenceid"),
		goqu.I("uug.reference_id").As("relationreferenceid"),
		goqu.I("uug.permission").As("permission"),
	).From(goqu.T("usergroup").As("ug")).
		Join(
			goqu.T(fmt.Sprintf("%s_%s_id_has_usergroup_usergroup_id", objType, objType)).As("uug"),
			goqu.On(goqu.Ex{"uug.usergroup_id": goqu.I("ug.id")})).
		Where(goqu.Ex{
			fmt.Sprintf("uug.%s_id", objType): objectId,
		}).ToSQL()

	stmt, err := dr.connection.Preparex(sql)
	if err != nil {
		log.Errorf("[501] failed to prepare statment: %v", err)
		return nil
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	res, err := stmt.Queryx(args...)

	if err != nil {
		log.Errorf("Failed to query object group by object id 403 [%v][%v] == %v", objType, objectId, err)
		return s
	}
	defer func(res *sqlx.Rows) {
		err := res.Close()
		if err != nil {
			log.Errorf("[478] failed to close result after value scan in defer")
		}
	}(res)

	for res.Next() {
		var g auth.GroupPermission
		err = res.StructScan(&g)
		g.ObjectReferenceId = refId
		if err != nil {
			log.Errorf("Failed to scan group permission 2: %v", err)
		}
		s = append(s, g)
	}
	return s

}

// CanBecomeAdmin Checks if the context user can invoke the become admin action
// checks if there is only 1 real user in the system
// No one can become admin once we have an adminstrator
func (dbResource *DbResource) CanBecomeAdmin() bool {

	adminRefId := dbResource.GetAdminReferenceId()
	if adminRefId == nil || len(adminRefId) == 0 {
		return true
	}

	return false

}

// GetUserAccountRowByEmail Returns the user account row of a user by looking up on email
func (d *DbResource) GetUserAccountRowByEmail(email string) (map[string]interface{}, error) {

	user, _, err := d.Cruds[USER_ACCOUNT_TABLE_NAME].GetRowsByWhereClause("user_account", nil, goqu.Ex{"email": email})

	if len(user) > 0 {

		return user[0], err
	}

	return nil, errors.New("no such user")

}

func (d *DbResource) GetUserPassword(email string) (string, error) {
	passwordHash := ""

	existingUsers, _, err := d.Cruds[USER_ACCOUNT_TABLE_NAME].GetRowsByWhereClause("user_account", nil, goqu.Ex{"email": email})
	if err != nil {
		return passwordHash, err
	}
	if len(existingUsers) < 1 {
		return passwordHash, errors.New("user not found")
	}

	passwordHash = existingUsers[0]["password"].(string)

	return passwordHash, err
}

// UserGroupNameToId Converts group name to the internal integer id

// should not be used since group names are not unique
// deprecated
func (dr *DbResource) UserGroupNameToId(groupName string) (uint64, error) {

	query, arg, err := statementbuilder.Squirrel.Select("id").From("usergroup").Where(goqu.Ex{"name": groupName}).ToSQL()
	if err != nil {
		return 0, err
	}
	stmt, err := dr.connection.Preparex(query)
	if err != nil {
		log.Errorf("[592] failed to prepare statment: %v", err)
		return 0, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	res := stmt.QueryRowx(arg...)
	if res.Err() != nil {
		return 0, res.Err()
	}

	var id uint64
	err = res.Scan(&id)

	return id, err
}

// BecomeAdmin make user the administrator and owner of everything
// Check CanBecomeAdmin before invoking this
func (dbResource *DbResource) BecomeAdmin(userId int64) bool {
	log.Printf("User: %d is going to become admin", userId)
	if !dbResource.CanBecomeAdmin() {
		return false
	}

	for _, crud := range dbResource.Cruds {

		if crud.model.GetName() == "user_account_user_account_id_has_usergroup_usergroup_id" {
			continue
		}

		if crud.model.HasColumn(USER_ACCOUNT_ID_COLUMN) {

			q, v, err := statementbuilder.Squirrel.
				Update(crud.model.GetName()).
				Set(goqu.Record{
					USER_ACCOUNT_ID_COLUMN: userId,
					"permission":           auth.DEFAULT_PERMISSION,
				}).ToSQL()
			if err != nil {
				log.Errorf("Query: %v", q)
				log.Errorf("Failed to create query to update to become admin: %v == %v", crud.model.GetName(), err)
				continue
			}

			_, err = dbResource.db.Exec(q, v...)
			if err != nil {
				log.Errorf("Query: %v", q)
				log.Errorf("	Failed to execute become admin update query: %v", err)
				continue
			}

		}
	}

	adminUsergroupId, err := dbResource.UserGroupNameToId("administrators")
	reference_id, err := uuid.NewV4()

	query, args, err := statementbuilder.Squirrel.Insert("user_account_user_account_id_has_usergroup_usergroup_id").
		Cols(USER_ACCOUNT_ID_COLUMN, "usergroup_id", "permission", "reference_id").
		Vals([]interface{}{userId, adminUsergroupId, int64(auth.DEFAULT_PERMISSION), reference_id.String()}).
		ToSQL()

	_, err = dbResource.db.Exec(query, args...)
	CheckErr(err, "Failed to add user to administrator usergroup: %v == %v", query, args)

	query, args, err = statementbuilder.Squirrel.Update("world").
		Set(goqu.Record{
			"permission":         int64(auth.DEFAULT_PERMISSION),
			"default_permission": int64(auth.DEFAULT_PERMISSION),
		}).
		Where(goqu.Ex{
			"table_name": goqu.Op{"notlike": "%_audit"},
		}).
		ToSQL()
	if err != nil {
		log.Errorf("Failed to create sql for updating world permissions: %v", err)
	}

	_, err = dbResource.db.Exec(query, args...)
	if err != nil {
		log.Errorf("Failed to update world permissions: %v", err)
	}

	query, args, err = statementbuilder.Squirrel.Update("world").
		Set(goqu.Record{
			"permission":         int64(auth.UserCreate | auth.GroupCreate),
			"default_permission": int64(auth.UserRead | auth.GroupRead),
		}).
		Where(goqu.Ex{
			"table_name": goqu.Op{"like": "%_audit"},
		}).ToSQL()
	if err != nil {
		log.Errorf("Failed to create sql for update world audit permissions: %v", err)
	}

	_, err = dbResource.db.Exec(query, args...)
	if err != nil {
		log.Errorf("Failed to world update audit permissions: %v", err)
	}

	query, args, err = statementbuilder.Squirrel.Update("action").
		Set(goqu.Record{"permission": int64(auth.UserRead | auth.UserExecute | auth.GroupCRUD | auth.GroupExecute | auth.GroupRefer)}).
		ToSQL()
	if err != nil {
		log.Errorf("Failed to create update action permission sql : %v", err)
	}

	_, err = dbResource.db.Exec(query, args...)
	if err != nil {
		log.Errorf("Failed to update action permissions : %v", err)
	}

	query, args, err = statementbuilder.Squirrel.Update("action").
		Set(goqu.Record{"permission": int64(auth.GuestPeek | auth.GuestExecute | auth.UserRead | auth.UserExecute | auth.GroupRead | auth.GroupExecute)}).
		Where(goqu.Ex{
			"action_name": "signin",
		}).
		ToSQL()
	if err != nil {
		log.Errorf("Failed to create update sign in action permission sql : %v", err)
	}

	_, err = dbResource.db.Exec(query, args...)
	if err != nil {
		log.Errorf("Failed to world update signin action  permissions: %v", err)
	}

	return true
}

func (dr *DbResource) GetRowPermission(row map[string]interface{}) PermissionInstance {

	refId, ok := row["reference_id"]
	if !ok {
		refId = row["id"]
	}
	rowType := row["__type"].(string)

	var perm PermissionInstance

	if rowType != "usergroup" {
		if row[USER_ACCOUNT_ID_COLUMN] != nil {
			uid, _ := row[USER_ACCOUNT_ID_COLUMN].(string)
			perm.UserId = uid
		} else {
			u, _ := dr.GetReferenceIdToObjectColumn(rowType, refId.(string), USER_ACCOUNT_ID_COLUMN)
			if u != nil {
				uid, _ := u.(string)
				perm.UserId = uid
			}
		}

	}

	loc := strings.Index(rowType, "_has_")
	//log.Printf("Location [%v]: %v", dr.model.GetName(), loc)

	if BeginsWith(rowType, "file.") || rowType == "none" {
		perm.UserGroupId = []auth.GroupPermission{
			{
				GroupReferenceId:    "",
				ObjectReferenceId:   "",
				RelationReferenceId: "",
				Permission:          auth.AuthPermission(auth.GuestRead),
			},
		}
		return perm
	}

	if loc == -1 && dr.Cruds[rowType].model.HasMany("usergroup") {

		perm.UserGroupId = dr.GetObjectUserGroupsByWhere(rowType, "reference_id", refId.(string))

	} else if rowType == "usergroup" {
		originalGroupId, _ := row["reference_id"]
		originalGroupIdStr := refId.(string)
		if originalGroupId != nil {
			originalGroupIdStr = originalGroupId.(string)
		}

		perm.UserGroupId = []auth.GroupPermission{
			{
				GroupReferenceId:    originalGroupIdStr,
				ObjectReferenceId:   refId.(string),
				RelationReferenceId: refId.(string),
				Permission:          auth.AuthPermission(dr.Cruds["usergroup"].model.GetDefaultPermission()),
			},
		}
	} else if loc > -1 {
		// this is a something belongs to a usergroup row
		//for colName, colValue := range row {
		//	if EndsWithCheck(colName, "_id") && colName != "reference_id" {
		//		if colName != "usergroup_id" {
		//			return dr.GetObjectPermissionByReferenceId(strings.Split(rowType, "_"+colName)[0], colValue.(string))
		//		}
		//	}
		//}

	}

	rowPermission := row["permission"]
	if rowPermission != nil {

		var err error
		i64, ok := rowPermission.(int64)
		if !ok {
			f64, ok := rowPermission.(float64)
			if !ok {
				i64, err = strconv.ParseInt(rowPermission.(string), 10, 64)
				//p, err := int64(row["permission"].(int))
				if err != nil {
					log.Errorf("Invalid cast :%v", err)
				}
			} else {
				i64 = int64(f64)
			}
		}

		perm.Permission = auth.AuthPermission(i64)
	} else {
		pe := dr.GetObjectPermissionByReferenceId(rowType, refId.(string))
		perm.Permission = pe.Permission
	}
	//log.Printf("Row permission: %v  ---------------- %v", perm, row)
	return perm
}

func (dr *DbResource) GetRowsByWhereClause(typeName string, includedRelations map[string]bool, where ...goqu.Ex) (
	[]map[string]interface{}, [][]map[string]interface{}, error) {

	stmt := statementbuilder.Squirrel.Select("*").From(typeName)

	for _, w := range where {
		stmt = stmt.Where(w)
	}

	s, q, err := stmt.ToSQL()

	//log.Printf("GetRowsByWhereClause: %v == [%v]", s)

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[839] failed to prepare statment: %v", err)
		return nil, nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	if err != nil {
		return nil, nil, err
	}
	defer func(rows *sqlx.Rows) {
		err := rows.Close()
		if err != nil {
			log.Errorf("[802] failed to close rows after scanning values in defer")
		}
	}(rows)

	m1, include, err := dr.ResultToArrayOfMap(rows, dr.Cruds[typeName].model.GetColumnMap(), includedRelations)

	return m1, include, err

}
func (dr *DbResource) GetRandomRow(typeName string, count uint) ([]map[string]interface{}, error) {

	randomFunc := "RANDOM() * "

	if dr.connection.DriverName() == "mysql" {
		randomFunc = "RAND() * "
	}

	// select id from world where id > RANDOM() * (SELECT MAX(id) FROM world) limit 15;
	maxSql, _, _ := goqu.Select(goqu.L("max(id)")).From(typeName).ToSQL()
	stmt := statementbuilder.Squirrel.Select("*").From(typeName).Where(goqu.Ex{
		"id": goqu.Op{"gte": goqu.L(randomFunc + " ( " + maxSql + " ) ")},
	}).Limit(count)

	s, q, err := stmt.ToSQL()

	//log.Printf("Select query: %v == [%v]", s, q)

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[885] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	if err != nil {
		return nil, err
	}
	defer func(rows *sqlx.Rows) {
		err := rows.Close()
		if err != nil {
			log.Errorf("[843] failed to close rows after value scan in defer")
		}
	}(rows)

	m1, _, err := dr.ResultToArrayOfMap(rows, dr.Cruds[typeName].model.GetColumnMap(), nil)

	return m1, err

}

func (dr *DbResource) GetUserMembersByGroupName(groupName string) []string {

	s, q, err := statementbuilder.Squirrel.
		Select("u.reference_id").
		From(goqu.T("user_account_user_account_id_has_usergroup_usergroup_id").As("uu")).
		LeftJoin(
			goqu.T("user_account").As("u"), goqu.On(goqu.Ex{
				"uu.user_account_id": goqu.I("u.id"),
			})).
		LeftJoin(
			goqu.T("usergroup").As("g"), goqu.On(goqu.Ex{
				"uu.usergroup_id": goqu.I("g.id"),
			})).
		Where(goqu.Ex{"g.name": groupName}).
		Order(goqu.I("uu.created_at").Asc()).ToSQL()
	if err != nil {
		log.Errorf("Failed to create sql query 749: %v", err)
		return []string{}
	}

	refIds := make([]string, 0)

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[936] failed to prepare statment: %v", err)
		return nil
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	if err != nil {
		log.Errorf("Failed to create sql query 757: %v", err)
		return []string{}
	}
	for rows.Next() {
		var refId string
		err = rows.Scan(&refId)
		CheckErr(err, "failed to scan ref id")
		refIds = append(refIds, refId)
	}

	return refIds

}

func (dr *DbResource) GetUserEmailIdByUsergroupId(usergroupId int64) string {

	s, q, err := statementbuilder.Squirrel.Select("u.email").From(goqu.T("user_account_user_account_id_has_usergroup_usergroup_id").As("uu")).
		LeftJoin(
			goqu.T(USER_ACCOUNT_TABLE_NAME).As("u"),
			goqu.On(goqu.Ex{
				"uu." + USER_ACCOUNT_ID_COLUMN: goqu.I("u.id"),
			}),
		).Where(goqu.Ex{"uu.usergroup_id": usergroupId}).
		Order(goqu.I("uu.created_at").Asc()).Limit(1).ToSQL()
	if err != nil {
		log.Errorf("Failed to create sql query 781: %v", err)
		return ""
	}

	var email string

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[981] failed to prepare statment: %v", err)
		return ""
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	err = stmt1.QueryRowx(q...).Scan(&email)
	if err != nil {
		log.Warnf("Failed to execute query 789: %v == %v", s, q)
		log.Warnf("Failed to scan user group id from the result 830: %v", err)
	}

	return email

}

func (dr *DbResource) GetUserById(userId int64) (map[string]interface{}, error) {

	user, _, err := dr.Cruds[USER_ACCOUNT_TABLE_NAME].GetSingleRowById("user_account", userId, nil)

	if len(user) > 0 {
		return user, err
	}

	return nil, errors.New("no such user")

	//type myStruct struct {
	//	UserName string
	//	EmailAddress string `db:"d"`
	//}
	//var email string
	//ds := statementbuilder.Squirrel.Select("email").From(goqu.T("user_account")).Where(goqu.Ex{"id": userId})
	//sql, args,err := ds.ToSQL()
	//
	//if err != nil {
	//	log.Errorf("Failed to create sql query 872: %v", err)
	//	return ""
	//}
	//
	//
	//rowx := dr.db.QueryRowx(sql, args...)
	//err = rowx.Scan(&email)
	//if err != nil {
	//	log.Errorf("Failed to create sql query 872: %v", err)
	//	return ""
	//}
	//return email

}

func (dr *DbResource) GetSingleRowByReferenceId(typeName string, referenceId string, includedRelations map[string]bool) (map[string]interface{}, []map[string]interface{}, error) {
	//log.Printf("Get single row by id: [%v][%v]", typeName, referenceId)
	s, q, err := statementbuilder.Squirrel.Select("*").From(typeName).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()
	if err != nil {
		log.Errorf("failed to create select query by ref id: %v", referenceId)
		return nil, nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1011] failed to prepare statment: %v", err)
		return nil, nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	if err != nil {
		log.Errorf("[940] failed to query single row by ref id: %v", err)
		return nil, nil, err
	}

	defer func() {
		if rows == nil {
			log.Printf("rows is already closed in get single row by reference id")
			return
		}
		err = rows.Close()
		CheckErr(err, "Failed to close rows after db query [%v]", s)
	}()

	resultRows, includeRows, err := dr.ResultToArrayOfMap(rows, dr.Cruds[typeName].model.GetColumnMap(), includedRelations)
	if err != nil {
		log.Printf("failed to ResultToArrayOfMap: %v", err)
		return nil, nil, err
	}

	if len(resultRows) < 1 {
		return nil, nil, fmt.Errorf("897 no such entity [%v][%v]", typeName, referenceId)
	}

	m := resultRows[0]
	n := includeRows[0]

	return m, n, err

}

func (dr *DbResource) GetSingleRowById(typeName string, id int64, includedRelations map[string]bool) (map[string]interface{}, []map[string]interface{}, error) {
	//log.Printf("Get single row by id: [%v][%v]", typeName, referenceId)
	s, q, err := statementbuilder.Squirrel.Select("*").From(typeName).Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		log.Errorf("Failed to create select query by id: %v", id)
		return nil, nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1063] failed to prepare statment: %v", err)
		return nil, nil, err
	}

	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	defer func(rows *sqlx.Rows) {
		err := rows.Close()
		if err != nil {
			log.Errorf("[989] failed to close rows after value scan in defer")
		}
	}(rows)
	resultRows, includeRows, err := dr.ResultToArrayOfMap(rows, dr.Cruds[typeName].model.GetColumnMap(), includedRelations)
	if err != nil {
		return nil, nil, err
	}

	if len(resultRows) < 1 {
		return nil, nil, fmt.Errorf("923 no such entity [%v][%v]", typeName, id)
	}

	m := resultRows[0]
	n := includeRows[0]

	return m, n, err

}

func (dr *DbResource) GetObjectByWhereClause(typeName string, column string, val interface{}) (map[string]interface{}, error) {
	s, q, err := statementbuilder.Squirrel.Select("*").From(typeName).Where(goqu.Ex{column: val}).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1106] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	row, err := stmt1.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(row *sqlx.Rows) {
		err := row.Close()
		if err != nil {
			log.Errorf("[1029] failed to close result after value scan in defer")
		}
	}(row)

	m, _, err := dr.ResultToArrayOfMap(row, dr.Cruds[typeName].model.GetColumnMap(), nil)

	if len(m) == 0 {
		log.Printf("No result found for [%v] [%v][%v]", typeName, column, val)
		return nil, errors.New(fmt.Sprintf("no [%s=%s] object found", column, val))
	}

	return m[0], err
}

func (dr *DbResource) GetIdToObject(typeName string, id int64) (map[string]interface{}, error) {
	s, q, err := statementbuilder.Squirrel.Select(goqu.C("*")).From(typeName).Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1146] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	row, err := stmt1.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(row *sqlx.Rows) {
		err := row.Close()
		if err != nil {
			log.Errorf("[1064] failed to close result after value scan in defer")
		}
	}(row)

	m, _, err := dr.ResultToArrayOfMap(row, dr.Cruds[typeName].model.GetColumnMap(), nil)

	if len(m) == 0 {
		log.Printf("No result found for [%v][%v]", typeName, id)
		return nil, err
	}

	return m[0], err
}

func (dr *DbResource) TruncateTable(typeName string, skipRelations bool) error {
	log.Printf("Truncate table: %v", typeName)

	if !skipRelations {

		var err error
		for _, rel := range dr.tableInfo.Relations {

			if rel.Relation == "belongs_to" {
				if rel.Subject == dr.tableInfo.TableName {
					// err = dr.TruncateTable(rel.Object, true)
				} else {
					err = dr.TruncateTable(rel.Object, true)
				}
			}
			if rel.Relation == "has_many" {
				err = dr.TruncateTable(rel.GetJoinTableName(), true)
			}
			if rel.Relation == "has_many_and_belongs_to_many" {
				err = dr.TruncateTable(rel.GetJoinTableName(), true)
			}
			if rel.Relation == "has_one" {
				if rel.Subject == dr.tableInfo.TableName {
					// err = dr.TruncateTable(rel.Object, true)
				} else {
					err = dr.TruncateTable(rel.Object, true)
				}
			}

			CheckErr(err, "Failed to truncate related table before truncate table [%v] [%v]", typeName, rel)
			err = nil
		}
	}

	s, q, err := statementbuilder.Squirrel.Delete(typeName).ToSQL()
	if err != nil {
		return err
	}

	_, err = dr.db.Exec(s, q...)

	return err

}

// Update the data and set the values using the data map without an validation or transformations
// Invoked by data import action
func (dr *DbResource) DirectInsert(typeName string, data map[string]interface{}) error {
	var err error

	columnMap := dr.Cruds[typeName].model.GetColumnMap()

	cols := make([]interface{}, 0)
	vals := make([]interface{}, 0)

	for columnName := range columnMap {
		colInfo, ok := dr.tableInfo.GetColumnByName(columnName)
		if !ok {
			log.Printf("No column named [%v]", columnName)
			continue
		}
		value := data[columnName]
		switch colInfo.ColumnType {
		case "datetime":
			if value != nil {
				valStr, ok := value.(string)
				if !ok {

				} else {

					value, err = dateparse.ParseLocal(valStr)
					if err != nil {
						log.Errorf("Failed to parse value as time, insert will fail [%v][%v]: %v", columnName, value, err)
						continue
					}
				}
			}
		}

		if columnName == "permission" {
			value = dr.tableInfo.DefaultPermission
		}

		cols = append(cols, columnName)
		vals = append(vals, value)

	}

	sqlString, args, err := statementbuilder.Squirrel.Insert(typeName).Cols(cols...).Vals(vals).ToSQL()

	if err != nil {
		return err
	}

	_, err = dr.db.Exec(sqlString, args...)
	if err != nil {
		log.Errorf("Failed SQL  [%v] [%v]", sqlString, args)
	}
	return err
}

// GetAllObjects Gets all rows from the table `typeName`
// Returns an array of Map object, each object has the column name to value mapping
// Utility method for loading all objects having low count
// Can be used by actions
func (dr *DbResource) GetAllObjects(typeName string) ([]map[string]interface{}, error) {
	s, q, err := statementbuilder.Squirrel.Select(goqu.L("*")).From(typeName).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1291] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	row, err := stmt1.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(row *sqlx.Rows) {
		err := row.Close()
		if err != nil {
			log.Errorf("[1204] failed to close result after value scan in defer")
		}
	}(row)

	m, _, err := dr.ResultToArrayOfMap(row, dr.Cruds[typeName].model.GetColumnMap(), nil)

	return m, err
}

// GetAllObjectsWithWhere Get all rows from the table `typeName`
// Returns an array of Map object, each object has the column name to value mapping
// Utility method for loading all objects having low count
// Can be used by actions
func (dr *DbResource) GetAllObjectsWithWhere(typeName string, where ...goqu.Ex) ([]map[string]interface{}, error) {
	query := statementbuilder.Squirrel.Select(goqu.L("*")).From(typeName)

	for _, w := range where {
		query = query.Where(w)
	}

	s, q, err := query.ToSQL()
	if err != nil {
		return nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1336] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	row, err := stmt1.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(row *sqlx.Rows) {
		err := row.Close()
		if err != nil {
			log.Errorf("[1244] failed to close result after value scan in defer")
		}
	}(row)

	m, _, err := dr.Cruds[typeName].ResultToArrayOfMap(row, dr.Cruds[typeName].model.GetColumnMap(), nil)

	return m, err
}

// GetAllRawObjects Get all rows from the table `typeName` without any processing of the response
// expect no "__type" column on the returned instances
// Returns an array of Map object, each object has the column name to value mapping
// Utility method for loading all objects having low count
// Can be used by actions
func (dr *DbResource) GetAllRawObjects(typeName string) ([]map[string]interface{}, error) {
	s, q, err := statementbuilder.Squirrel.Select(goqu.L("*")).From(typeName).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1376] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	row, err := stmt1.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(row *sqlx.Rows) {
		err := row.Close()
		if err != nil {
			log.Errorf("[1279] failed to close result after value scan in defer")
		}
	}(row)

	m, err := RowsToMap(row, typeName)

	return m, err
}

// GetReferenceIdToObject Loads an object of type `typeName` using a reference_id
// Used internally, can be used by actions
func (dr *DbResource) GetReferenceIdToObject(typeName string, referenceId string) (map[string]interface{}, error) {

	k := fmt.Sprintf("rio-%v-%v", typeName, referenceId)
	if OlricCache != nil {
		v, err := OlricCache.Get(k)
		if err == nil {
			return v.(map[string]interface{}), nil
		}
	}

	//log.Printf("Get Object by reference id [%v][%v]", typeName, referenceId)
	s, q, err := statementbuilder.Squirrel.Select("*").From(typeName).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1423] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	//log.Printf("Get object by reference id sql: %v", s)
	row, err := stmt1.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func() {
		err = row.Close()
		CheckErr(err, "[1314] Failed to close row after querying single row")
	}()

	results, _, err := dr.ResultToArrayOfMap(row, dr.Cruds[typeName].model.GetColumnMap(), nil)
	if err != nil {
		return nil, err
	}

	//log.Printf("Have to return first of %d results", len(results))
	if len(results) == 0 {
		return nil, fmt.Errorf("no such object 1161 [%v][%v]", typeName, referenceId)
	}
	if OlricCache != nil {
		_ = OlricCache.PutIfEx(k, results[0], 5*time.Second, olric.IfNotFound)
	}

	return results[0], err
}

// GetReferenceIdToObjectColumn Loads an object of type `typeName` using a reference_id
// Used internally, can be used by actions
func (dr *DbResource) GetReferenceIdToObjectColumn(typeName string, referenceId string, columnToSelect string) (interface{}, error) {
	//log.Printf("Get Object by reference id [%v][%v]", typeName, referenceId)
	s, q, err := statementbuilder.Squirrel.Select(columnToSelect).From(typeName).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()
	if err != nil {
		return nil, err
	}

	//log.Printf("Get object by reference id sql: %v", s)

	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1473] failed to prepare statment for get object by reference id: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	row, err := stmt.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func() {
		err = row.Close()
		CheckErr(err, "Failed to close row after querying single row")
	}()

	results, _, err := dr.ResultToArrayOfMap(row, dr.Cruds[typeName].model.GetColumnMap(), nil)
	if err != nil {
		return nil, err
	}

	//log.Printf("Have to return first of %d results", len(results))
	if len(results) == 0 {
		return nil, fmt.Errorf("no such object 1197 [%v][%v]", typeName, referenceId)
	}

	return results[0][columnToSelect], err
}

// Load rows from the database of `typeName` with a where clause to filter rows
// Converts the queries to sql and run query with where clause
// Returns list of reference_ids
func (dr *DbResource) GetReferenceIdByWhereClause(typeName string, queries ...goqu.Ex) ([]string, error) {
	builder := statementbuilder.Squirrel.Select("reference_id").From(typeName)

	for _, qu := range queries {
		builder = builder.Where(qu)
	}

	s, q, err := builder.ToSQL()
	//log.Debugf("reference id by where query: %v", s)

	if err != nil {
		return nil, err
	}

	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1525] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	res, err := stmt.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(res *sqlx.Rows) {
		err := res.Close()
		if err != nil {
			log.Errorf("[1296] Failed to close rows after query")
		}
	}(res)

	ret := make([]string, 0)
	for res.Next() {
		var s string
		err := res.Scan(&s)
		if err != nil {
			log.Errorf("[1305] failed to scan result into variable")
			return nil, err
		}
		ret = append(ret, s)
	}

	return ret, err

}

// GetIdByWhereClause Loads rows from the database of `typeName` with a where clause to filter rows
// Converts the queries to sql and run query with where clause
// Returns  list of internal database integer ids
func (dr *DbResource) GetIdByWhereClause(typeName string, queries ...goqu.Ex) ([]int64, error) {
	builder := statementbuilder.Squirrel.Select("id").From(typeName)

	for _, qu := range queries {
		builder = builder.Where(qu)
	}

	s, q, err := builder.ToSQL()
	//log.Debugf("reference id by where query: %v", s)

	if err != nil {
		return nil, err
	}

	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1581] failed to prepare statment: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	res, err := stmt.Queryx(q...)

	if err != nil {
		return nil, err
	}
	defer func(res *sqlx.Rows) {
		err := res.Close()
		if err != nil {
			log.Errorf("[1454] failed to close rows after value scan in defer")
		}
	}(res)

	ret := make([]int64, 0)
	for res.Next() {
		var s int64
		err := res.Scan(&s)
		if err != nil {
			log.Errorf("[1463] failed to scan value after query")
			return nil, err
		}
		ret = append(ret, s)
	}

	return ret, err

}

// GetIdToReferenceId Looks up an integer id and return a string reference id of an object of type `typeName`
func (dr *DbResource) GetIdToReferenceId(typeName string, id int64) (string, error) {

	k := fmt.Sprintf("itr-%v-%v", typeName, id)
	if OlricCache != nil {
		v, err := OlricCache.Get(k)
		if err == nil {
			return v.(string), nil
		}
	}

	s, q, err := statementbuilder.Squirrel.Select("reference_id").From(typeName).Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return "", err
	}

	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1636] failed to prepare statment: %v", err)
		return "", err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	var str string
	row := stmt.QueryRowx(q...)
	err = row.Scan(&str)
	if OlricCache != nil {
		OlricCache.PutIfEx(k, str, 1*time.Minute, olric.IfNotFound)
	}
	return str, err

}




// Lookup an string reference id and return a internal integer id of an object of type `typeName`
func (dr *DbResource) GetReferenceIdToId(typeName string, referenceId string) (int64, error) {

	var id int64
	s, q, err := statementbuilder.Squirrel.Select("id").From(typeName).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()
	if err != nil {
		return 0, err
	}
	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1666] failed to prepare statment: %v", err)
		return 0, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	err = stmt.QueryRowx(q...).Scan(&id)
	return id, err

}

// Lookup an string reference id and return a internal integer id of an object of type `typeName`
func (dr *DbResource) GetReferenceIdListToIdList(typeName string, referenceId []string) (map[string]int64, error) {

	idMap := make(map[string]int64)
	s, q, err := statementbuilder.Squirrel.Select("id", "reference_id").
		From(typeName).Where(goqu.Ex{"reference_id": referenceId}).ToSQL()
	if err != nil {
		return idMap, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1694] failed to prepare statment: %v", err)
		return nil, err
	}

	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	if err != nil {
		return idMap, err
	}
	for rows.Next() {
		var id1 int64
		var id2 string
		err = rows.Scan(&id1, &id2)
		idMap[id2] = id1
	}

	return idMap, err
}

// GetIdListToReferenceIdList Lookups an string internal integer id and return a reference id of an object of type `typeName`
func (dr *DbResource) GetIdListToReferenceIdList(typeName string, ids []int64) (map[int64]string, error) {

	idMap := make(map[int64]string)
	s, q, err := statementbuilder.Squirrel.Select("reference_id", "id").
		From(typeName).Where(goqu.Ex{"id": ids}).ToSQL()
	if err != nil {
		return idMap, err
	}

	stmt1, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1731] failed to prepare statment: %v", err)
		return nil, err
	}

	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(q...)
	if err != nil {
		return idMap, err
	}
	for rows.Next() {
		var id1 string
		var id2 int64
		err = rows.Scan(&id1, &id2)
		CheckErr(err, "[1581] failed to scan value after query: %v[%v]", typeName, ids)
		idMap[id2] = id1
	}

	return idMap, err
}

// GetSingleColumnValueByReferenceId select "column" from "typeName" where matchColumn in (values)
// returns list of values of the column
func (dr *DbResource) GetSingleColumnValueByReferenceId(
	typeName string, selectColumn []interface{}, matchColumn string, values []string) ([]interface{}, error) {

	s, q, err := statementbuilder.Squirrel.Select(selectColumn...).From(typeName).Where(goqu.Ex{matchColumn: values}).ToSQL()
	if err != nil {
		return nil, err
	}

	stmt, err := dr.connection.Preparex(s)
	if err != nil {
		log.Errorf("[1768] failed to prepare statment for permission select: %v", err)
		return nil, err
	}
	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt)

	rows, err := stmt.Queryx(q...)
	if err != nil {
		return nil, err
	}

	defer func(rows *sqlx.Rows) {
		err := rows.Close()
		if err != nil {
			log.Errorf("[1483] failed to close result after value scan")
		}
	}(rows)
	returnValues := make([]interface{}, 0)

	for rows.Next() {
		var val interface{}
		err = rows.Scan(&val)
		if err != nil {
			log.Errorf("[1620] failed to scan value after query")
			break
		}
		returnValues = append(returnValues, val)
	}

	return returnValues, nil
}

// RowsToMap converts the result of db.QueryRowx => rows to array of data
// can be used on any *sqlx.Rows and assign a typeName
func RowsToMap(rows *sqlx.Rows, typeName string) ([]map[string]interface{}, error) {

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	responseArray := make([]map[string]interface{}, 0)

	for rows.Next() {

		rc := NewMapStringScan(columns)
		err := rc.Update(rows)
		if err != nil {
			return responseArray, err
		}

		dbRow := rc.Get()
		dbRow["__type"] = typeName
		responseArray = append(responseArray, dbRow)
	}

	return responseArray, nil
}

// ResultToArrayOfMap converts the result of db.QueryRowx => rows to array of data
// fetches the related objects also
// expects columnMap to be fetched from rows
// check usage in exiting source for example
// includeRelationMap can be nil to include none or map[string]bool{"*": true} to include all relations
// can be used on any *sqlx.Rows
func (dr *DbResource) ResultToArrayOfMap(rows *sqlx.Rows, columnMap map[string]api2go.ColumnInfo, includedRelationMap map[string]bool) ([]map[string]interface{}, [][]map[string]interface{}, error) {

	//finalArray := make([]map[string]interface{}, 0)
	if includedRelationMap == nil {
		includedRelationMap = make(map[string]bool)
	}

	responseArray, err := RowsToMap(rows, dr.model.GetName())
	if err != nil {
		return responseArray, nil, err
	}

	objectCache := make(map[string]interface{})
	referenceIdCache := make(map[string]string)
	includes := make([][]map[string]interface{}, 0)

	for _, row := range responseArray {
		localInclude := make([]map[string]interface{}, 0)

		for key, val := range row {
			//log.Printf("Key: [%v] == %v", key, val)

			columnInfo, ok := columnMap[key]
			if !ok {
				continue
			}

			if val != nil && columnInfo.ColumnType == "datetime" {
				stringVal, ok := val.(string)
				if ok {
					parsedValue, _, err := fieldtypes.GetTime(stringVal)
					if err != nil {
						parsedValue, _, err := fieldtypes.GetDateTime(stringVal)
						if InfoErr(err, "Failed to parse date time from [%v]: %v", columnInfo.ColumnName, stringVal) {
							row[key] = nil
						} else {
							row[key] = parsedValue
						}
					} else {
						row[key] = parsedValue
					}
				}
			}

			if !columnInfo.IsForeignKey {
				continue
			}

			if val == "" || val == nil {
				continue
			}

			namespace := columnInfo.ForeignKeyData.Namespace
			//log.Printf("Resolve foreign key from [%v][%v][%v]", columnInfo.ForeignKeyData.DataSource, namespace, val)
			switch columnInfo.ForeignKeyData.DataSource {
			case "self":

				referenceIdInt, ok := val.(int64)
				if !ok {
					stringIntId := val.(string)
					referenceIdInt, err = strconv.ParseInt(stringIntId, 10, 64)
					CheckErr(err, "Failed to convert string id to int id")
				}
				cacheKey := fmt.Sprintf("%v-%v", namespace, referenceIdInt)
				objCached, ok := objectCache[cacheKey]
				if ok {
					localInclude = append(localInclude, objCached.(map[string]interface{}))
					continue
				}

				idCacheKey := fmt.Sprintf("%s_%d", namespace, referenceIdInt)
				refId, ok := referenceIdCache[idCacheKey]

				if !ok {
					refId, err = dr.GetIdToReferenceId(namespace, referenceIdInt)
					referenceIdCache[idCacheKey] = refId
				}

				if err != nil {
					log.Errorf("Failed to get ref id for [%v][%v]: %v", namespace, val, err)
					continue
				}
				row[key] = refId

				if includedRelationMap != nil && (includedRelationMap[namespace] || includedRelationMap[columnInfo.ColumnName] || includedRelationMap["*"]) {
					obj, err := dr.GetIdToObject(namespace, referenceIdInt)
					obj["__type"] = namespace

					if err != nil {
						log.Errorf("Failed to get ref object for [%v][%v]: %v", namespace, val, err)
					} else {
						localInclude = append(localInclude, obj)
					}
				}

			case "cloud_store":
				referenceStorageInformation := val.(string)
				//log.Printf("Resolve files from cloud store: %v", referenceStorageInformation)
				foreignFilesList := make([]map[string]interface{}, 0)
				err := json.Unmarshal([]byte(referenceStorageInformation), &foreignFilesList)
				CheckErr(err, "Failed to obtain list of file information")
				if err != nil {
					continue
				}

				returnFileList := make([]map[string]interface{}, 0)

				for _, file := range foreignFilesList {

					if file["type"] == "x-crdt/yjs" && !includedRelationMap["x-crdt/yjs"] {
						continue
					}

					if file["path"] != nil && file["name"] != nil && len(file["path"].(string)) > 0 {
						file["src"] = file["path"].(string) + "/" + file["name"].(string)
					} else if file["name"] != nil {
						file["src"] = file["name"].(string)
					} else {
						log.Errorf("File entry is missing name and path [%v][%v]", dr.TableInfo().TableName, key)
					}
					returnFileList = append(returnFileList, file)
				}

				row[key] = returnFileList
				//log.Printf("set row[%v]  == %v", key, foreignFilesList)
				if includedRelationMap[columnInfo.ColumnName] || includedRelationMap["*"] {

					resolvedFilesList, err := dr.GetFileFromLocalCloudStore(dr.TableInfo().TableName, columnInfo.ColumnName, returnFileList)
					CheckErr(err, "Failed to resolve file from cloud store")
					row[key] = resolvedFilesList
					for _, file := range resolvedFilesList {
						file["__type"] = columnInfo.ColumnType
						localInclude = append(localInclude, file)
					}

				}
			default:
				log.Errorf("Undefined data source: %v", columnInfo.ForeignKeyData.DataSource)
				continue
			}

		}

		for _, relation := range dr.tableInfo.Relations {

			if !(includedRelationMap[relation.GetObjectName()] || includedRelationMap[relation.GetSubjectName()]) {
				continue
			}

			if relation.Subject == dr.tableInfo.TableName {
				// fetch objects

				switch relation.Relation {
				case "has_one":
					// nothing to do here
					break
				case "belongs_to":
					// nothing to do here
					break
				case "has_many":

					fallthrough
				case "has_many_and_belongs_to_many":
					query, args, err := statementbuilder.Squirrel.
						Select(goqu.I(relation.GetObjectName()+".id")).
						From(goqu.T(relation.GetSubject()).As(relation.GetSubjectName())).
						Join(
							goqu.T(relation.GetJoinTableName()).As(relation.GetJoinTableName()),
							goqu.On(goqu.Ex{
								relation.GetJoinTableName() + "." + relation.GetSubjectName(): goqu.I(relation.GetSubjectName() + ".id"),
							}),
						).
						Join(
							goqu.T(relation.GetObject()).As(relation.GetObjectName()),
							goqu.On(goqu.Ex{
								fmt.Sprintf("%v.%v", relation.GetJoinTableName(), relation.GetObjectName()): goqu.I(relation.GetObjectName() + ".id"),
							}),
						).
						Where(goqu.Ex{
							relation.GetSubjectName() + ".reference_id": row["reference_id"],
						}).Order(goqu.I(relation.GetJoinTableName() + ".created_at").Desc()).Limit(50).ToSQL()
					if err != nil {
						log.Printf("Failed to build query 1474: %v", err)
					}

					stmt1, err := dr.connection.Preparex(query)
					if err != nil {
						log.Errorf("[2023] failed to prepare statment: %v", err)
					}
					defer func(stmt1 *sqlx.Stmt) {
						err := stmt1.Close()
						if err != nil {
							log.Errorf("failed to close prepared statement: %v", err)
						}
					}(stmt1)

					rows, err := stmt1.Queryx(args...)
					if err != nil {
						log.Printf("Failed to query 1482: %v", err)
					}

					ids := make([]int64, 0)

					for rows.Next() {
						includeRow := int64(0)
						err = rows.Scan(&includeRow)
						if err != nil {
							log.Printf("[1857] failed to scan include row: %v", err)
							continue
						}
						ids = append(ids, includeRow)
					}

					rows.Close()

					includes1, err := dr.Cruds[relation.GetObject()].GetAllObjectsWithWhere(relation.GetObject(), goqu.Ex{
						"id": ids,
					})

					_, ok := row[relation.GetObjectName()]
					if !ok {
						row[relation.GetObjectName()] = make([]string, 0)
					}

					for _, incl := range includes1 {
						row[relation.GetObjectName()] = append(row[relation.GetObjectName()].([]string), incl["reference_id"].(string))
					}

					localInclude = append(localInclude, includes1...)

					break
				}

			} else {
				// fetch subjects

				switch relation.Relation {
				case "has_one":

					fallthrough
				case "belongs_to":

					query, args, err := statementbuilder.Squirrel.
						Select(goqu.I(relation.GetSubjectName()+".id")).
						From(goqu.T(relation.GetObject()).As(relation.GetObjectName())).
						Join(
							goqu.T(relation.GetSubject()).As(relation.GetSubjectName()),
							goqu.On(goqu.Ex{
								fmt.Sprintf("%v.%v", relation.GetSubjectName(), relation.GetObjectName()): goqu.I(relation.GetObjectName() + ".id"),
							}),
						).
						Where(goqu.Ex{
							relation.GetObjectName() + ".reference_id": row["reference_id"],
						}).Order(goqu.I(relation.GetSubjectName() + ".created_at").Desc()).Limit(50).ToSQL()

					if err != nil {
						log.Printf("Failed to build query 1533: %v", err)
					}

					stmt1, err := dr.connection.Preparex(query)
					if err != nil {
						log.Errorf("[2097] failed to prepare statment: %v", err)
					}
					defer func(stmt1 *sqlx.Stmt) {
						err := stmt1.Close()
						if err != nil {
							log.Errorf("failed to close prepared statement: %v", err)
						}
					}(stmt1)

					includedSubject, err := stmt1.Queryx(args...)
					if err != nil {
						log.Printf("Failed to query 1538: %v", includedSubject.Err())
						continue
					}
					includedSubjectId := []int64{}

					for includedSubject.Next() {
						var subId int64
						err = includedSubject.Scan(&subId)
						includedSubjectId = append(includedSubjectId, subId)
					}
					CheckErr(err, "[2133] failed to scan included subject id")
					err = includedSubject.Close()
					CheckErr(err, "[2135] failed to close rows")

					if len(includedSubjectId) < 1 {
						continue
					}

					localSubjectInclude, err := dr.Cruds[relation.GetSubject()].GetAllObjectsWithWhere(relation.GetSubject(), goqu.Ex{
						"id": includedSubjectId,
					})
					CheckErr(err, "[1923] failed to get object by od")

					_, ok := row[relation.GetSubjectName()]
					if !ok {
						row[relation.GetSubjectName()] = make([]string, 0)
					}

					for _, incl := range localSubjectInclude {
						row[relation.GetSubjectName()] = append(row[relation.GetSubjectName()].([]string), incl["reference_id"].(string))
					}

					localInclude = append(localInclude, localSubjectInclude...)

					break
				case "has_many":

					fallthrough
				case "has_many_and_belongs_to_many":
					query, args, err := statementbuilder.Squirrel.
						Select(goqu.I(relation.GetSubjectName()+".id")).
						From(goqu.T(relation.GetObject()).As(relation.GetObjectName())).
						Join(
							goqu.T(relation.GetJoinTableName()).As(relation.GetJoinTableName()),
							goqu.On(goqu.Ex{
								relation.GetJoinTableName() + "." + relation.GetObjectName(): goqu.I(relation.GetObjectName() + ".id"),
							}),
						).
						Join(
							goqu.T(relation.GetSubject()).As(relation.GetSubjectName()),
							goqu.On(goqu.Ex{
								fmt.Sprintf("%v.%v", relation.GetJoinTableName(), relation.GetSubjectName()): goqu.I(relation.GetSubjectName() + ".id"),
							}),
						).
						Where(goqu.Ex{
							relation.GetObjectName() + ".reference_id": row["reference_id"],
						}).Order(goqu.I(relation.GetJoinTableName() + ".created_at").Desc()).Limit(50).ToSQL()
					if err != nil {
						log.Printf("Failed to build query 1474: %v", err)
					}

					stmt1, err := dr.connection.Preparex(query)
					if err != nil {
						log.Errorf("[2155] failed to prepare statment: %v", err)
					}
					defer func(stmt1 *sqlx.Stmt) {
						err := stmt1.Close()
						if err != nil {
							log.Errorf("failed to close prepared statement: %v", err)
						}
					}(stmt1)

					rows, err := stmt1.Queryx(args...)

					if err != nil {
						log.Printf("Failed to query 1482: %v", err)
						continue
					}

					ids := make([]int64, 0)

					for rows.Next() {
						includeRow := int64(0)
						err = rows.Scan(&includeRow)
						if err != nil {
							log.Printf("[1966] failed to scan include row: %v", err)
							continue
						}
						ids = append(ids, includeRow)
					}
					rows.Close()

					includes1, err := dr.Cruds[relation.GetObject()].GetAllObjectsWithWhere(relation.GetSubject(), goqu.Ex{
						"id": ids,
					})

					_, ok := row[relation.GetSubjectName()]
					if !ok {
						row[relation.GetSubjectName()] = make([]string, 0)
					}

					for _, incl := range includes1 {
						row[relation.GetSubjectName()] = append(row[relation.GetSubjectName()].([]string), incl["reference_id"].(string))
					}

					localInclude = append(localInclude, includes1...)

					break
				}

			}

		}

		includes = append(includes, localInclude)

	}

	return responseArray, includes, nil
}

// convert the result of db.QueryRowx => rows to array of data
// can be used on any *sqlx.Rows and assign a typeName
// calls RowsToMap with the current model name
func (dr *DbResource) ResultToArrayOfMapRaw(rows *sqlx.Rows, columnMap map[string]api2go.ColumnInfo) ([]map[string]interface{}, error) {

	//finalArray := make([]map[string]interface{}, 0)

	responseArray, err := RowsToMap(rows, dr.model.GetName())
	if err != nil {
		return responseArray, err
	}

	return responseArray, nil
}

// resolve a file column from data in column to actual file on a cloud store
// returns a map containing the metadata of the file and the file contents as base64 encoded
// can be sent to browser to invoke downloading js and data urls
func (resource *DbResource) GetFileFromCloudStore(data api2go.ForeignKeyData, filesList []map[string]interface{}) (resp []map[string]interface{}, err error) {

	cloudStore, err := resource.GetCloudStoreByName(data.Namespace)
	if err != nil {
		return resp, err
	}

	for _, fileItem := range filesList {
		newFileItem := make(map[string]interface{})

		for key, val := range fileItem {
			newFileItem[key] = val
		}

		fileName := fileItem["name"].(string)
		filePath := cloudStore.RootPath + "/" + data.KeyName + "/" + fileName
		bytes, err := ioutil.ReadFile(filePath)
		CheckErr(err, "Failed to read file on storage %s", filePath)
		if err != nil {
			continue
		}
		newFileItem["reference_id"] = fileItem["name"]
		newFileItem["contents"] = base64.StdEncoding.EncodeToString(bytes)
		resp = append(resp, newFileItem)
	}
	return resp, nil
}

// resolve a file column from data in column to actual file on a cloud store
// returns a map containing the metadata of the file and the file contents as base64 encoded
// can be sent to browser to invoke downloading js and data urls
func (resource *DbResource) GetFileFromLocalCloudStore(tableName string, columnName string, filesList []map[string]interface{}) (resp []map[string]interface{}, err error) {

	assetFolder, ok := resource.AssetFolderCache[tableName][columnName]
	if !ok {
		return nil, errors.New("not a synced folder")
	}

	for _, fileItem := range filesList {
		newFileItem := make(map[string]interface{})

		for key, val := range fileItem {
			newFileItem[key] = val
		}

		if fileItem["src"] == nil {
			log.Printf("file has no source: [%v][%v]", tableName, columnName)
			continue
		}

		filePath := fileItem["src"].(string)
		filePath = strings.ReplaceAll(filePath, "/", string(os.PathSeparator))
		if filePath[0] != os.PathSeparator {
			filePath = string(os.PathSeparator) + filePath
		}
		bytes, err := ioutil.ReadFile(assetFolder.LocalSyncPath + filePath)
		CheckErr(err, "Failed to read file on storage [%v]: %v", assetFolder.LocalSyncPath, filePath)
		if err != nil {
			continue
		}
		newFileItem["reference_id"] = fileItem["name"]
		newFileItem["contents"] = base64.StdEncoding.EncodeToString(bytes)
		resp = append(resp, newFileItem)
	}
	return resp, nil
}
