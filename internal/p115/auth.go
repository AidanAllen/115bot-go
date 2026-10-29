package p115

import (
	"context"
	"encoding/json"
	"fmt"
)

// CheckLogin 校验登录态并返回账号信息。
// 依据：my.115.com/?ct=ajax&ac=nav 返回 {state, data:{user_id,user_name,...}, error}。
// 该结构已用真实请求验证；登录失效时 error 为「请先登录,后操作！」。
func (c *Client) CheckLogin(ctx context.Context) (*UserInfo, error) {
	body, err := c.get(ctx, c.ep.Nav)
	if err != nil {
		return nil, err
	}
	env, err := decodeEnvelope("nav", body)
	if err != nil {
		return nil, err
	}

	var d NavData
	if len(env.Data) > 0 && string(env.Data) != "[]" {
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return nil, fmt.Errorf("解析账号信息失败: %w", err)
		}
	}
	if d.UserID.String() == "" || d.UserID.String() == "0" {
		return nil, ErrLoginRequired
	}

	uid, _ := d.UserID.Int64()
	return &UserInfo{UserID: uid, UserName: d.UserName, Raw: d}, nil
}
