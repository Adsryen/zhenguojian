package sign6

import (
	"crypto/md5"
	"fmt"
	"testing"
)

// TestArgusFieldBinding 校验 f13/f14 的取值与抓包一致。
func TestArgusFieldBinding(t *testing.T) {
	query := `_rticket=1793618286780&ac=wifi&aid=8662&app_name=novelread&channel=update_64&device_brand=Redmi&device_id=1237425293633402&device_platform=android&device_type=25053RT47C&dpi=520&iid=1237425293637498&language=zh&manifest_version_code=73532&os=android&os_api=36&os_version=16&resolution=1280%2A2772&ssmix=a&update_version_code=73532&version_code=73532&version_name=7.3.5.32`
	body := `{"biz_param":{"detail_page_version":0,"device_level":1,"disable_digg_stat":false,"need_all_video_definition":true,"need_mp4_align":false,"use_os_player":false,"use_server_dns":false,"video_platform":1024},"mixed_video_id_map":{"1004":["7648600565701692440"]}}`
	md := md5.Sum([]byte(body))
	got13 := fmt.Sprintf("%x", sm3Sum(md[:])[:6])
	got14 := fmt.Sprintf("%x", sm3Sum([]byte(query))[:6])
	fmt.Printf("f13=%s (want 0bf84fcfa3e1) f14=%s (want e4f24d207b87)\n", got13, got14)
	if got13 != "0bf84fcfa3e1" || got14 != "e4f24d207b87" {
		t.Fatal("field binding mismatch")
	}
}
