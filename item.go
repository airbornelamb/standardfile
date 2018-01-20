package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/deckarep/golang-set"
	// "github.com/kisielk/sqlstruct"
	"github.com/satori/go.uuid"
	"github.com/tectiv3/standardfile/db"
)

// Item - is an item type
type Item struct {
	Uuid         string    `json:"uuid"`
	User_uuid    string    `json:"user_uuid"`
	Content      string    `json:"content"`
	Content_type string    `json:"content_type"`
	Enc_item_key string    `json:"enc_item_key"`
	Auth_hash    string    `json:"auth_hash"`
	Deleted      bool      `json:"deleted"`
	Created_at   time.Time `json:"created_at"`
	Updated_at   time.Time `json:"updated_at"`
}

type it interface {
	create() error
	update() error
	delete() error
}

//Items - is an items slice
type Items []Item

//SyncRequest - type for incoming sync request
type SyncRequest struct {
	Items       Items  `json:"items"`
	SyncToken   string `json:"sync_token"`
	CursorToken string `json:"cursor_token"`
	Limit       int    `json:"limit"`
}

type unsaved struct {
	Item
	error
}

//SyncResponse - type for response
type SyncResponse struct {
	Retrieved   Items     `json:"retrieved_items"`
	Saved       Items     `json:"saved_items"`
	Unsaved     []unsaved `json:"unsaved"`
	SyncToken   string    `json:"sync_token"`
	CursorToken string    `json:"cursor_token,omitempty"`
}

const minConflictInterval = 20.0

//LoadValue - hydrate struct from map
func (r *SyncRequest) LoadValue(name string, value []string) {
	switch name {
	case "items":
		r.Items = Items{}
	case "sync_token":
		r.SyncToken = value[0]
	case "cursor_token":
		r.CursorToken = value[0]
	case "limit":
		r.Limit, _ = strconv.Atoi(value[0])
	}
}

//LoadValue - hydrate struct from map
func (this *Item) LoadValue(name string, value []string) {
	switch name {
	case "uuid":
		this.Uuid = value[0]
	case "user_uuid":
		this.User_uuid = value[0]
	case "content":
		this.Content = value[0]
	case "enc_item_key":
		this.Enc_item_key = value[0]
	case "content_type":
		this.Content_type = value[0]
	case "auth_hash":
		this.Content_type = value[0]
	case "deleted":
		this.Deleted = (value[0] == "true")
	}
}

//Save - save current item into DB
func (this *Item) save() error {
	if this.Uuid == "" || !this.Exists() {
		return this.create()
	}
	return this.update()
}

func (this *Item) create() error {
	if this.Uuid == "" {
		this.Uuid = uuid.NewV4().String()
	}
	this.Created_at = time.Now()
	this.Updated_at = time.Now()
	Log("Create:", this.Uuid)
	return db.Query("INSERT INTO `items` (`uuid`, `user_uuid`, content,  content_type, enc_item_key, auth_hash, deleted, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?)", this.Uuid, this.User_uuid, this.Content, this.Content_type, this.Enc_item_key, this.Auth_hash, this.Deleted, this.Created_at, this.Updated_at)
}

func (this *Item) update() error {
	this.Updated_at = time.Now()
	Log("Update:", this.Uuid)
	return db.Query("UPDATE `items` SET `content`=?, `enc_item_key`=?, `auth_hash`=?, `deleted`=?, `updated_at`=? WHERE `uuid`=? AND `user_uuid`=?", this.Content, this.Enc_item_key, this.Auth_hash, this.Deleted, this.Updated_at, this.Uuid, this.User_uuid)
}

func (this *Item) delete() error {
	if this.Uuid == "" {
		return fmt.Errorf("Trying to delete unexisting item")
	}
	this.Content = ""
	this.Enc_item_key = ""
	this.Auth_hash = ""
	this.Updated_at = time.Now()

	return db.Query("UPDATE `items` SET `content`='', `enc_item_key`='', `auth_hash`='',`deleted`=1, `updated_at`=? WHERE `uuid`=? AND `user_uuid`=?", this.Updated_at, this.Uuid, this.User_uuid)
}

func (this Item) copy() (Item, error) {
	this.Uuid = uuid.NewV4().String()
	this.Updated_at = time.Now()
	err := this.create()
	if err != nil {
		Log(err)
		return Item{}, err
	}
	return this, nil
}

//Exists - checks if current user exists in DB
func (this Item) Exists() bool {
	if this.Uuid == "" {
		return false
	}
	uuid, err := db.SelectFirst("SELECT `uuid` FROM `items` WHERE `uuid`=?", this.Uuid)

	if err != nil {
		Log(err)
		return false
	}
	Log("Exists:", uuid)
	return uuid != ""
}

//LoadByUUID - loads item info from DB
func (this *Item) LoadByUUID(uuid string) bool {
	_, err := db.SelectStruct("SELECT * FROM `items` WHERE `uuid`=?", this, uuid)

	if err != nil {
		Log(err)
		return false
	}

	return true
}

//GetTokenFromTime - generates sync token for current time
func GetTokenFromTime(date time.Time) string {
	return base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf("1:%d", date.UnixNano())))
}

//GetTimeFromToken - retreive datetime from sync token
func GetTimeFromToken(token string) time.Time {
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		Log(err)
		return time.Now()
	}
	parts := strings.Split(string(decoded), ":")
	str, err := strconv.Atoi(parts[1])
	if err != nil {
		Log(err)
		return time.Now()
	}
	return time.Time(time.Unix(0, int64(str)))
}

//SyncItems - sync manager
func (user User) SyncItems(request SyncRequest) (SyncResponse, error) {

	response := SyncResponse{
		Retrieved:   Items{},
		Saved:       Items{},
		Unsaved:     []unsaved{},
		SyncToken:   GetTokenFromTime(time.Now()),
		CursorToken: "",
	}

	if request.Limit == 0 {
		request.Limit = 100000
	}
	var err error
	var cursorTime time.Time
	Log("Get items")
	response.Retrieved, cursorTime, err = user.getItems(request)
	// Log("Retreived items:", response.Retrieved)
	if err != nil {
		return response, err
	}
	if !cursorTime.IsZero() {
		response.CursorToken = GetTokenFromTime(cursorTime)
	}
	Log("Save incoming items", request)
	response.Saved, response.Unsaved, err = request.Items.save(user.Uuid)
	if err != nil {
		return response, err
	}
	if len(response.Saved) > 0 {
		response.SyncToken = GetTokenFromTime(response.Saved[0].Updated_at)
		// Check for conflicts
		Log("Conflicts check")
		response.Saved.checkForConflicts(&response.Retrieved)
	}
	return response, nil
}

func (items Items) checkForConflicts(existing *Items) {
	Log("Saved len:", len(items))
	Log("Retreived len:", len(*existing))
	saved := mapset.NewSet()
	for _, item := range items {
		saved.Add(item.Uuid)
	}
	retreived := mapset.NewSet()
	for _, item := range *existing {
		retreived.Add(item.Uuid)
	}
	conflicts := saved.Intersect(retreived)
	Log("Conflicts", conflicts)
	// saved items take precedence, retrieved items are duplicated with a new uuid
	for _, uuid := range conflicts.ToSlice() {
		// if changes are greater than minConflictInterval seconds apart, create conflicted copy, otherwise discard conflicted
		Log(uuid)
		savedCopy := items.find(uuid.(string))
		retreivedCopy := existing.find(uuid.(string))

		if savedCopy.isConflictedWith(retreivedCopy) {
			log.Printf("Creating conflicted copy of %v\n", uuid)
			dupe, err := retreivedCopy.copy()
			if err != nil {
				Log(err)
			} else {
				*existing = append(*existing, dupe)
			}
		}
		existing.delete(uuid.(string))
	}
}

func (this Item) isConflictedWith(copy Item) bool {
	diff := math.Abs(float64(this.Updated_at.Unix() - copy.Updated_at.Unix()))
	Log("Conflict diff, min interval:", diff, minConflictInterval)
	return diff > minConflictInterval
}

func (items Items) save(userUUID string) (Items, []unsaved, error) {
	savedItems := Items{}
	unsavedItems := []unsaved{}

	if len(items) == 0 {
		return savedItems, unsavedItems, nil
	}

	for _, item := range items {
		var err error
		item.User_uuid = userUUID
		if item.Deleted {
			err = item.delete()
		} else {
			err = item.save()
		}
		if err != nil {
			unsavedItems = append(unsavedItems, unsaved{item, err})
			Log("Unsaved:", item)
		} else {
			item.load() //reloading item info from DB
			savedItems = append(savedItems, item)
			Log("Saved:", item)
		}
	}
	return savedItems, unsavedItems, nil
}

func (this *Item) load() bool {
	return this.LoadByUUID(this.Uuid)
}

func (user User) getItems(request SyncRequest) (items Items, cursorTime time.Time, err error) {
	if request.CursorToken != "" {
		Log("loadItemsFromDate")
		items, err = user.loadItemsFromDate(GetTimeFromToken(request.CursorToken))
	} else if request.SyncToken != "" {
		Log("loadItemsOlder")
		items, err = user.loadItemsOlder(GetTimeFromToken(request.SyncToken))
	} else {
		Log("loadItems")
		items, err = user.loadItems(request.Limit)
		if len(items) > 0 {
			cursorTime = items[len(items)-1].Updated_at
		}
	}
	return items, cursorTime, err
}

func (u User) loadItemsFromDate(date time.Time) ([]Item, error) {
	items := []Item{}
	err := db.Select("SELECT * FROM `items` WHERE `user_uuid`=? AND `updated_at` >= ? ORDER BY `updated_at` DESC", &items, u.Uuid, date)
	return items, err
}

func (u User) loadItemsOlder(date time.Time) ([]Item, error) {
	items := []Item{}
	err := db.Select("SELECT * FROM `items` WHERE `user_uuid`=? AND `updated_at` > ? ORDER BY `updated_at` DESC", &items, u.Uuid, date)
	return items, err
}

func (u User) loadItems(limit int) ([]Item, error) {
	items := []Item{}
	err := db.Select("SELECT * FROM `items` WHERE `user_uuid`=? ORDER BY `updated_at` DESC", &items, u.Uuid)
	return items, err
}

func (items Items) find(uuid string) Item {
	for _, item := range items {
		if item.Uuid == uuid {
			return item
		}
	}
	return Item{}
}

func (items *Items) delete(uuid string) {
	position := 0
	for i, item := range *items {
		if item.Uuid == uuid {
			position = i
			break
		}
	}
	(*items) = (*items)[:position:position]
}
