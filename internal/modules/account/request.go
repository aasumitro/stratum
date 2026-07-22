package account

// upsertProfileRequest deliberately has no Email field — the profile's
// email always comes from the caller's verified JWT claim, never the
// request body, so a client can't set account.users.email to an arbitrary
// value another part of the system might trust for authorization.
type upsertProfileRequest struct {
	FullName  string `json:"full_name" binding:"omitempty,max=255"`
	AvatarURL string `json:"avatar_url" binding:"omitempty,url,max=2048"`
}

type updateProfileRequest struct {
	FullName  string `json:"full_name" binding:"omitempty,max=255"`
	AvatarURL string `json:"avatar_url" binding:"omitempty,url,max=2048"`
}
