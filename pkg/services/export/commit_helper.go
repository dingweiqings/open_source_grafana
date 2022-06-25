package export

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/grafana/grafana/pkg/services/sqlstore"
)

type commitHelper struct {
	ctx     context.Context
	repo    *git.Repository
	work    *git.Worktree
	orgDir  string // includes the orgID
	workDir string // same as the worktree root
	orgID   int64
	users   map[int64]*userInfo
}

type commitBody struct {
	fpath string // absolute
	body  []byte
}

type commitOptions struct {
	body    []commitBody
	when    time.Time
	userID  int64
	comment string
}

func (ch *commitHelper) initOrg(sql *sqlstore.SQLStore, orgID int64) error {
	return sql.WithDbSession(ch.ctx, func(sess *sqlstore.DBSession) error {
		sess.Table("user").
			Join("INNER", "org_user", "user.id = org_user.user_id").
			Asc("user.created").
			Where("user.org_id = ?", orgID)

		rows := make([]*userInfo, 0)
		err := sess.Find(&rows)
		if err != nil {
			return err
		}

		lookup := make(map[int64]*userInfo, len(rows))
		for _, row := range rows {
			lookup[row.ID] = row
		}
		ch.users = lookup
		ch.orgID = orgID
		return err
	})
}

func (ch *commitHelper) add(opts commitOptions) error {
	for _, b := range opts.body {
		if !strings.HasPrefix(b.fpath, ch.orgDir) {
			return fmt.Errorf("invalid path, must be within the root folder")
		}

		// make sure the parent exists
		if err := os.MkdirAll(path.Dir(b.fpath), 0750); err != nil {
			return err
		}

		err := ioutil.WriteFile(b.fpath, b.body, 0644)
		if err != nil {
			return err
		}

		sub := b.fpath[len(ch.workDir)+1:]
		_, err = ch.work.Add(sub)
		if err != nil {
			status, e2 := ch.work.Status()
			if e2 != nil {
				return fmt.Errorf("error adding: %s (invalud work status: %s)", sub, e2.Error())
			}
			fmt.Printf("STATUS: %+v\n", status)
			return fmt.Errorf("unable to add file: %s (%d)", sub, len(b.body))
		}
	}

	user, ok := ch.users[opts.userID]
	if !ok {
		user = &userInfo{
			Name:  "admin",
			Email: "admin@unknown.org",
		}
	}
	sig := user.getAuthor()
	if opts.when.Unix() > 10 {
		sig.When = opts.when
	}

	copts := &git.CommitOptions{
		Author: &sig,
	}

	_, err := ch.work.Commit(opts.comment, copts)
	return err
}

type userInfo struct {
	ID               int64     `json:"-" xorm:"id"`
	Login            string    `json:"login"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	Password         string    `json:"password"`
	Salt             string    `json:"salt"`
	Role             string    `json:"role"` // org role
	Theme            string    `json:"-"`    // managed in preferences
	Created          time.Time `json:"-"`    // managed in git or external source
	Updated          time.Time `json:"-"`    // managed in git or external source
	IsDisabled       bool      `json:"disabled" xorm:"is_disabled"`
	IsServiceAccount bool      `json:"serviceAccount" xorm:"is_service_account"`
	LastSeenAt       time.Time `json:"-" xorm:"last_seen_at"`
}

func (u *userInfo) getAuthor() object.Signature {
	return object.Signature{
		Name:  firstRealStringX(u.Name, u.Login, u.Email, "?"),
		Email: firstRealStringX(u.Email, u.Login, u.Name, "?"),
	}
}

func firstRealStringX(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return "?"
}
