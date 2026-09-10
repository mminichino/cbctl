package capella

import (
	"fmt"

	"github.com/mminichino/cbctl/internal/logging"
)

// User manages Capella organization users.
type User struct {
	Org   *Organization
	Email string
	User  *CapellaOrgUserData
}

// NewUser resolves the configured Capella account user.
func NewUser(org *Organization) (*User, error) {
	u := &User{Org: org}
	c := org.Client
	var err error
	switch {
	case c.hasAccountEmail():
		u.Email = c.AccountEmail
		u.User, err = u.GetByEmail(u.Email)
	case c.hasAccountID():
		u.User, err = u.GetByID(c.AccountID)
		if err == nil && u.User != nil {
			u.Email = u.User.Email
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if u.User == nil || u.Email == "" {
		return nil, &UserNotConfiguredError{Message: "Capella user not configured"}
	}
	logging.Info("User ID: %s (%s)", u.User.ID, u.User.Email)
	return u, nil
}

// Endpoint returns users collection path.
func (u *User) Endpoint() string {
	return fmt.Sprintf("%s/%s/users", u.Org.Endpoint(), u.Org.Organization.ID)
}

// ListUsers lists organization users (paged).
func (u *User) ListUsers() ([]CapellaOrgUserData, error) {
	items, err := u.Org.Client.GetPaged(u.Endpoint(), 100)
	if err != nil {
		return nil, apiError(u.Org.Client.lastStatus, u.Org.Client.lastBody, "User List Error", err)
	}
	return DecodePaged[CapellaOrgUserData](items)
}

// GetByID returns a user by ID.
func (u *User) GetByID(userID string) (*CapellaOrgUserData, error) {
	if u.User != nil && u.User.ID == userID {
		return u.User, nil
	}
	var data CapellaOrgUserData
	if err := u.Org.Client.GetJSON(u.Endpoint()+"/"+userID, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("User ID not found")
		}
		return nil, apiError(u.Org.Client.lastStatus, u.Org.Client.lastBody, "User Get Error", err)
	}
	return &data, nil
}

// GetByEmail returns a user by email.
func (u *User) GetByEmail(email string) (*CapellaOrgUserData, error) {
	if email == "" {
		email = u.Email
	}
	if email == "" {
		return nil, fmt.Errorf("No email configured")
	}
	if u.User != nil && u.User.Email == email {
		return u.User, nil
	}
	users, err := u.ListUsers()
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Email == email {
			return &users[i], nil
		}
	}
	return nil, fmt.Errorf("No user for email: %s", email)
}

// ProjectIDs returns project resource IDs from the user.
func (u *User) ProjectIDs() []string {
	if u.User == nil {
		return nil
	}
	var ids []string
	for _, r := range u.User.Resources {
		if r.Type == "project" && r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// SetProjectOwnership patches the user with projectOwner on the project.
func (u *User) SetProjectOwnership(projectID string) error {
	if u.User == nil || u.User.ID == "" {
		return apiError(0, "", "User not resolved", nil)
	}
	payload := []JSONPatchOp{{
		Op:   "add",
		Path: "/resources/" + projectID,
		Value: map[string]any{
			"type":  "project",
			"id":    projectID,
			"roles": []string{"projectOwner"},
		},
	}}
	if err := u.Org.Client.PatchJSON(u.Endpoint()+"/"+u.User.ID, payload, nil); err != nil {
		return apiError(u.Org.Client.lastStatus, u.Org.Client.lastBody, "User Set Error", err)
	}
	return nil
}
