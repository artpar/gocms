package resource

import (
	"errors"
	"fmt"
	uuid "github.com/artpar/go.uuid"
	"github.com/daptin/daptin/server/auth"
	"github.com/daptin/daptin/server/statementbuilder"
	"github.com/doug-martin/goqu/v9"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"time"
)

func(dr *DbResource) GetRpath(userId int64)(string,error){

	rPath := ""

	cal, _, err := dr.Cruds["calendar"].GetRowsByWhereClause("calendar", nil, goqu.Ex{"id": userId})
	if err != nil {
		return rPath, err
	}
	if len(cal) < 1 {
		return rPath, errors.New("calendar not found")
	}

	rPath = cal[0]["rpath"].(string)

	return rPath, err
}

func(dr *DbResource) GetCalendarId(rPath string, userId int64)(string,error){

	rowID := ""

	cal, _, err := dr.Cruds["calendar"].GetRowsByWhereClause("calendar", nil, goqu.Ex{"user_account_id": userId},goqu.Ex{"rpath": rPath})
	if err != nil {
		return rowID, err
	}
	if len(cal) < 1 {
		return rowID, errors.New("calendar not found")
	}

	rowID = cal[0]["id"].(string)

	return rowID, err
}

func (dr *DbResource) DeleteCalendarEvent(UserId int64, rPath string) error {

	cal, err := dr.Cruds["calendar"].GetAllObjectsWithWhere("calendar",
		goqu.Ex{
			"id": UserId,
			"rpath": rPath,
		},
	)

	if err != nil || len(cal) == 0 {
		return errors.New("caldav resource does not exist")
	}

	query, args, err := statementbuilder.Squirrel.Delete("calendar").Where(goqu.Ex{"rpath": cal[0]["rpath"]}).ToSQL()
	if err != nil {
		return err
	}

	_, err = dr.db.Exec(query, args...)
	if err != nil {
		return err
	}


	return err

}

func(dr *DbResource) GetModTime(rPath string, userId int64)(time.Time,error){
	modified := time.Now()

	cal, _, err := dr.Cruds["calendar"].GetRowsByWhereClause("calendar", nil, goqu.Ex{"id": userId},goqu.Ex{"rpath": rPath})
	if err != nil {
		return modified, err
	}

	if len(cal) < 1 {
		return modified, errors.New("calendar not found")
	}

	modified = cal[0]["updated_at"].(time.Time)

	return modified, err
}

func(dr *DbResource) GetContent(rPath string, userId int64)(string,error){
	content := ""

	cal, _, err := dr.Cruds["calendar"].GetRowsByWhereClause("calendar", nil, goqu.Ex{"id": userId},goqu.Ex{"rpath": rPath})
	if err != nil {
		return content, err
	}

	if len(cal) < 1 {
		return content, errors.New("content not found")
	}

	content = cal[0]["content"].(string)

	return content, err
}

func (d *DbResource) UpdateResource(rPath, newContent string) error {

	cal, err := d.Cruds["calendar"].GetAllObjectsWithWhere("calendar",
		goqu.Ex{
			"rpath": rPath,
		},
	)
	if err != nil || len(cal) == 0 {
		return errors.New("calendar Event does not exist")
	}

	query, args, err := statementbuilder.Squirrel.
		Update("calendar").
		Set(goqu.Record{"content": newContent, "updated_at": time.Now()}).
		Where(goqu.Ex{"id": cal[0]["id"]}).ToSQL()
	if err != nil {
		return err
	}

	_, err = d.db.Exec(query, args...)

	return err

}

func (dr *DbResource) InsertResource(rPath, content string, userId int64) error{
	referenceId, _ := uuid.NewV4()
	permission := dr.model.GetDefaultPermission()

	fmt.Println("USERID", userId)

	query, args, err := statementbuilder.Squirrel.Insert("calendar").
		Cols("rpath", "content", "user_account_id", "reference_id", "permission").
		Vals([]interface{}{rPath, content, userId, referenceId.String(), permission}).
		ToSQL()

	if err != nil {
		return err
	}

	_, err = dr.db.Exec(query, args...)
	if err != nil {
		CheckErr(err, "Failed to Insert Calendar Resource: %v == %v", query, args)
		return err
	}

	return nil
}

func (dr *DbResource) GetCalendarIdByAccountId(typeName string, userId int64) (int64, error) {

	s, q, err := statementbuilder.Squirrel.Select("id").From(typeName).Where(goqu.Ex{"user_account_id": userId}).ToSQL()
	if err != nil {
		return 0, err
	}

	var id int64
	row := dr.db.QueryRowx(s, q...)
	err = row.Scan(&id)
	return id, err

}

func(dr *DbResource) GetUserGroupById(typename string, userID int64, referenceId string)([]auth.GroupPermission, error){

	query, args1, err := auth.UserGroupSelectQuery.Where(goqu.Ex{"uug.id": userID}).ToSQL()

	stmt1, err := dr.Cruds[typename].connection.Preparex(query)
	if err != nil {
		log.Errorf("[143] failed to prepare statment: %v", err)
	}

	defer func(stmt1 *sqlx.Stmt) {
		err := stmt1.Close()
		if err != nil {
			log.Errorf("failed to close prepared statement: %v", err)
		}
	}(stmt1)

	rows, err := stmt1.Queryx(args1...)
	userGroups := make([]auth.GroupPermission, 0)

	if err != nil {
		log.Errorf("Failed to get user group permissions: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p auth.GroupPermission
			err = rows.StructScan(&p)
			p.ObjectReferenceId = referenceId
			if err != nil {
				log.Errorf("failed to scan group permission struct: %v", err)
				continue
			}
			userGroups = append(userGroups, p)
		}

	}

	return userGroups, nil
}