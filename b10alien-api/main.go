package main

import (
    "net/http"
"github.com/gin-gonic/gin"
    "github.com/keploy/go-sdk/integrations/kgin/v1" // NEW LINE
    "github.com/keploy/go-sdk/keploy" // NEW LINE
)

type b10alien struct {
	ID      string `json:"id"`
    Name    string `json:"name"`
    Power   int64  `json:"power"`
    Special string `json:"special"`
}

var b10aliens = []b10alien{
	{ID: "1", Name: "Alien-X", Power: 90000, Special: "intelligence, power, speed, hax"},
	{ID: "2", Name: "Swamp-Fire", Power: 2000, Special: "fire, plant, invulnerabilityxl"},
	{ID: "3", Name: "Xlr8", Power: 1500, Special: "speed,mobility"},
	{ID: "4", Name: "Jet-Ray", Power: 1900, Special: "flight, speed, lazer"},
	{ID: "5", Name: "Ben", Power: 50, Special: "turn into alien, weakest, useless"},
}

func home(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{ // H is a shortcut for map[string]interface{}
        "instructions": "Add '/b10aliens' to the link",
    })
}

func getB10aliens(c *gin.Context) {
    // Printing all the Aliens available in the data
    c.JSON(http.StatusOK, b10aliens)
}

func addB10alien(c *gin.Context) {
    var newB10alien b10alien
    
   if err := c.ShouldBindJSON(&newB10alien); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":   true,
            "message": "Bad Request",
        })
        return
    }
    
    // Add the new superhero to the slice.
    b10aliens = append(b10aliens, newB10alien)
    
    // Serializing the struct as JSON and adding it to the response
    c.JSON(http.StatusCreated, newB10alien)
}

func editB10alien(c *gin.Context) {
    id := c.Param("id")
    
    // Creating a new object to structure superhero
    var editB10alien b10alien
// Call BindJSON to bind the received JSON to newSuperhero
    // BindJSON adds the data provided by user to newSuperhero
    // This is kind of "try catch" concept
    if err := c.ShouldBindJSON(&editB10alien); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":   true,
            "message": "Bad Request",
        })
        return
    }
    
    for i, hero := range b10aliens {
        if hero.ID == id {
            b10aliens[i].Name = editB10alien.Name
            b10aliens[i].Power = editB10alien.Power
            b10aliens[i].Special = editB10alien.Special
c.JSON(http.StatusOK, editB10alien)
            return
        }
    }
// If the above statement doesn't return anything, that means the id is invalid
    c.JSON(http.StatusBadRequest, gin.H{
        "error":   true,
        "message": "Invalid",
    })
}

func removeB10alien(c *gin.Context) {
    id := c.Param("id")
    for i, alien := range b10aliens {
        if alien.ID == id {
            // arr := [100, 200, 300, 400, 500]
            // arr[:2] = [100, 200]
            // arr[2:] = [300, 400, 500]
            // arr[2+1:] = [400. 500]
            // [100, 200][400, 500]
            b10aliens = append(b10aliens[:i], b10aliens[i+1:]...) // ... is required when writing 2 slices in append function
c.JSON(http.StatusOK, gin.H{
                "message": "Item Deleted",
            })
            return
        }
    }
// If the above statement doesn't return anything, that means the id is invalid
    c.JSON(http.StatusBadRequest, gin.H{
        "error":   true,
        "message": "Invalid",
    })
}

func main() {
    // Keploy configurations
    port := "8080"
    keploy := keploy.New(keploy.Config{
        App: keploy.AppConfig{
            Name: "b10alien-api",
            Port: port,
        },
        Server: keploy.ServerConfig{
        URL: "http://localhost:8081/api",
        },
    })
	router := gin.Default()
    kgin.GinV1(keploy, router)

    router.GET("/", home)
	router.GET("/b10aliens", getB10aliens)
	router.POST("/b10aliens", addB10alien)
	router.PUT("/b10aliens/:id", editB10alien)
    router.DELETE("/b10aliens/:id", removeB10alien)
    router.Run(":8080")
}
