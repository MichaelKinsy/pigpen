package pi_subagents

import "os/user"

// osUserName is os.userInfo().username.
func osUserName() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
