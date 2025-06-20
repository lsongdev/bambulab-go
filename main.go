package main

import (
	"log"

	"github.com/lsongdev/bambulab-go/bambulab"
)

func main() {
	c := bambulab.NewClient()
	// resp, err := c.Login(&bambulab.Credential{
	// 	Account: "song940@gmail.com",
	// 	// Password: "lsong940",
	// 	Code: "712362",
	// })
	// if err != nil {
	// 	panic(err)
	// }
	// 2025/06/20 14:42:04 &{AAC23s0DhD2q8PB5ndMV9--VcY6TvJlcSQVOmg6pjnGCu3dZNbQUQ7rIp43S5yTPwJbWIXSQN5IgpeBnt3cy7rRdIqmbdM5GRLaW8i62HEQoom8HzoW5H6Ymk9PVT1l_upxMpTpKM1W-3HLU AAC23s0DhD2q8PB5ndMV9--VcY6TvJlcSQVOmg6pjnGCu3dZNbQUQ7rIp43S5yTPwJbWIXSQN5IgpeBnt3cy7rRdIqmbdM5GRLaW8i62HEQoom8HzoW5H6Ymk9PVT1l_upxMpTpKM1W-3HLU  7776000}
	// c.AccessToken = "AAC23s0DhD2q8PB5ndMV9--VcY6TvJlcSQVOmg6pjnGCu3dZNbQUQ7rIp43S5yTPwJbWIXSQN5IgpeBnt3cy7rRdIqmbdM5GRLaW8i62HEQoom8HzoW5H6Ymk9PVT1l_upxMpTpKM1W-3HLU"

	// resp1, err := c.Login(&bambulab.Credential{
	// 	Account: "18510100102",
	// 	// Password: "lsong940!",
	// 	Code: "808975",
	// })
	// if err != nil {
	// 	panic(err)
	// }
	// 2025/06/20 17:18:47 &{AACsYzLDNxyC1u1HbeiegeMaONXU4UVkKlLcrBkJOitmmBMMAGnIZbDp9T5CV_ZsyxawsbMfyjYRBKBWPRGvqjBoSiLBJVszMbYKIVvsS_MlSEkhvxZ3TsfSGbePGgv4OdqWFEWlE_VhnoUP AACsYzLDNxyC1u1HbeiegeMaONXU4UVkKlLcrBkJOitmmBMMAGnIZbDp9T5CV_ZsyxawsbMfyjYRBKBWPRGvqjBoSiLBJVszMbYKIVvsS_MlSEkhvxZ3TsfSGbePGgv4OdqWFEWlE_VhnoUP  7776000}
	// log.Println(resp1)
	c.AccessToken = "AACsYzLDNxyC1u1HbeiegeMaONXU4UVkKlLcrBkJOitmmBMMAGnIZbDp9T5CV_ZsyxawsbMfyjYRBKBWPRGvqjBoSiLBJVszMbYKIVvsS_MlSEkhvxZ3TsfSGbePGgv4OdqWFEWlE_VhnoUP"

	profile, err := c.GetProfile()
	if err != nil {
		panic(err)
	}
	log.Println(profile)

	resp2, err := c.ListDevices()
	if err != nil {
		panic(err)
	}
	for _, device := range resp2.Devices {
		log.Println(device.ID, device.Name, device.Online, device.Model, device.Product, device.AccessCode)
	}
}
