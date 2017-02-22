package demoinfocs

import (
	"bytes"
	"fmt"
	bs "github.com/markus-wa/demoinfocs-golang/bitstream"
	"github.com/markus-wa/demoinfocs-golang/common"
	"github.com/markus-wa/demoinfocs-golang/events"
	"github.com/markus-wa/demoinfocs-golang/st"
	"strconv"
	"strings"
)

const maxOsPath = 260

const (
	playerWeaponPrefix    = "m_hMyWeapons."
	playerWeaponPrePrefix = "bcc_nonlocaldata."
)

const (
	teamName_Unassigned = "Unassigned"
	teamName_Spectator  = "Spectator"
	teamName_Terrorist  = "TERRORIST"
	teamName_Ct         = "CT"
)

// ParseHeader attempts to parse the header of the demo.
// Panics if the filestamp doesn't match HL2DEMO
func (p *Parser) ParseHeader() {
	var h common.DemoHeader
	h.Filestamp = p.bitreader.ReadCString(8)
	h.Protocol = p.bitreader.ReadSignedInt(32)
	h.NetworkProtocol = p.bitreader.ReadSignedInt(32)
	h.ServerName = p.bitreader.ReadCString(maxOsPath)
	h.ClientName = p.bitreader.ReadCString(maxOsPath)
	h.MapName = p.bitreader.ReadCString(maxOsPath)
	h.GameDirectory = p.bitreader.ReadCString(maxOsPath)
	h.PlaybackTime = p.bitreader.ReadFloat()
	h.PlaybackTicks = p.bitreader.ReadSignedInt(32)
	h.PlaybackFrames = p.bitreader.ReadSignedInt(32)
	h.SignonLength = p.bitreader.ReadSignedInt(32)

	if h.Filestamp != "HL2DEMO" {
		panic("Shit's fucked mate (Invalid File-Type; expecting HL2DEMO)")
	}

	p.header = &h
	p.eventDispatcher.Dispatch(events.HeaderParsedEvent{Header: h})
}

// ParseToEnd attempts to parse the demo until the end.
// Aborts and panics when the cancelToken is set to true.
// May panic if the demo is corrupt in some way.
func (p *Parser) ParseToEnd(cancelToken *bool) {
	for cancelToken == nil || !*cancelToken {
		if !p.ParseNextTick() {
			break
		}
	}
	if cancelToken != nil && *cancelToken {
		panic("Parser.ParseToEnd() has been cancelled")
	}
}

// ParseNextTick attempts to parse the next tick.
// Returns true unless the demo command 'stop' was encountered.
// Panics if header hasn't been parsed yet - see Parser.ParseHeader().
func (p *Parser) ParseNextTick() bool {
	if p.header == nil {
		panic("Tried to parse tick before parsing header")
	}
	b := p.parseTick()

	for k, rp := range p.rawPlayers {
		if rp == nil {
			continue
		}

		if pl := p.players[k]; pl != nil {
			newPlayer := false
			if p.connectedPlayers[rp.UserId] == nil {
				p.connectedPlayers[rp.UserId] = pl
				newPlayer = true
			}

			pl.Name = rp.Name
			pl.SteamId = rp.XUID
			pl.IsBot = rp.IsFakePlayer
			pl.AdditionalPlayerInformation = &p.additionalPlayerInfo[pl.EntityId]

			if pl.IsAlive() {
				pl.LastAlivePosition = pl.Position
			}

			if newPlayer && pl.SteamId != 0 {
				p.eventDispatcher.Dispatch(events.PlayerBindEvent{Player: pl})
			}
		}
	}

	p.eventDispatcher.Dispatch(events.TickDoneEvent{})

	if !b {
		close(p.msgQueue)
	}

	return b
}

func (p *Parser) parseTick() bool {
	cmd := demoCommand(p.bitreader.ReadSingleByte())

	// Tick number
	p.ingameTick = p.bitreader.ReadSignedInt(32)
	// Skip 'player slot'
	p.bitreader.ReadSingleByte()

	p.currentTick++

	switch cmd {
	case dc_Synctick:
		// Ignore

	case dc_Stop:
		return false

	case dc_ConsoleCommand:
		// Skip
		p.bitreader.BeginChunk(p.bitreader.ReadSignedInt(32) * 8)
		p.bitreader.EndChunk()

	case dc_DataTables:
		p.bitreader.BeginChunk(p.bitreader.ReadSignedInt(32) * 8)
		p.stParser.ParsePacket(p.bitreader)
		p.bitreader.EndChunk()

		p.mapEquipment()
		p.bindEntities()

	case dc_StringTables:
		p.bitreader.BeginChunk(p.bitreader.ReadSignedInt(32) * 8)
		p.parseStringTables()
		p.bitreader.EndChunk()

	case dc_UserCommand:
		// Skip
		p.bitreader.ReadInt(32)
		p.bitreader.BeginChunk(p.bitreader.ReadSignedInt(32) * 8)
		p.bitreader.EndChunk()

	case dc_Signon:
		fallthrough
	case dc_Packet:
		// Booooring
		parseCommandInfo(p.bitreader)
		p.bitreader.ReadInt(32) // SeqNrIn
		p.bitreader.ReadInt(32) // SeqNrOut

		p.bitreader.BeginChunk(p.bitreader.ReadSignedInt(32) * 8)
		p.parsePacket()
		p.bitreader.EndChunk()

	default:
		panic("Canny handle it anymoe (command " + string(cmd) + "unknown)")
	}
	return true
}

func (p *Parser) parseStringTables() {
	tables := int(p.bitreader.ReadSingleByte())
	for i := 0; i < tables; i++ {
		tableName := p.bitreader.ReadString()
		p.parseSingleStringTable(tableName)
	}
}

func (p *Parser) parseSingleStringTable(name string) {
	strings := p.bitreader.ReadSignedInt(16)
	for i := 0; i < strings; i++ {
		stringName := p.bitreader.ReadString()
		if len(stringName) >= 100 {
			panic("Someone said that Roy said I should panic")
		}
		if p.bitreader.ReadBit() {
			userDataSize := p.bitreader.ReadSignedInt(16)
			data := p.bitreader.ReadBytes(userDataSize)
			switch name {
			case "userinfo":
				r := bs.NewBitReader(bytes.NewReader(data), bs.SmallBuffer)
				player := common.ParsePlayerInfo(r)
				r.Close()
				pid, err := strconv.ParseInt(stringName, 10, 64)
				if err != nil {
					panic("Couldn't parse id from string")
				}
				p.rawPlayers[int(pid)] = player

			case "instancebaseline":
				pid, err := strconv.ParseInt(stringName, 10, 64)
				if err != nil {
					panic("Couldn't parse id from string")
				}
				p.instanceBaselines[int(pid)] = data

			case "modelprecache":
				p.modelPreCache = append(p.modelPreCache, stringName)

			default:
				// Irrelevant table
			}
		}
	}
	// Client side stuff, dgaf
	if p.bitreader.ReadBit() {
		strings2 := p.bitreader.ReadSignedInt(16)
		for i := 0; i < strings2; i++ {
			p.bitreader.ReadString()
			if p.bitreader.ReadBit() {
				p.bitreader.ReadBytes(p.bitreader.ReadSignedInt(16))
			}
		}
	}
}

func (p *Parser) mapEquipment() {
	for _, sc := range p.stParser.ServerClasses() {
		if len(sc.BaseClasses) > 6 && sc.BaseClasses[6].Name == "CWeaponCSBase" {
			var err error
			if len(sc.BaseClasses) > 7 {
				switch sc.BaseClasses[7].Name {
				case "CWeaponCSBaseGun":
					// Most guns
					p.equipmentMapping[sc] = common.MapEquipment(strings.ToLower(sc.DTName[9:]))
				case "CBaseCSGrenade":
					// Nades
					p.equipmentMapping[sc] = common.MapEquipment(strings.ToLower(sc.DTName[3:]))
				}
			} else if sc.Name == "CKnife" || (len(sc.BaseClasses) > 6 && sc.BaseClasses[6].Name == "CKnife") {
				p.equipmentMapping[sc] = common.EE_Knife
			} else {
				switch sc.Name {
				case "CC4":
					p.equipmentMapping[sc] = common.EE_Bomb

				case "CWeaponNOVA":
					fallthrough
				case "CWeaponSawedoff":
					fallthrough
				case "CWeaponXM1014":
					p.equipmentMapping[sc] = common.MapEquipment(strings.ToLower(sc.Name[7:]))
				}
			}
			if err != nil {
				fmt.Println(err.Error())
			}
		}
	}
}

// Everything down here feels fucked

func (p *Parser) bindEntities() {
	p.handleTeamScores()
	p.handleBombSites()
	p.handlePlayers()
	p.handleWeapons()
}

func (p *Parser) handleTeamScores() {
	p.stParser.FindServerClassByName("CCSTeam").RegisterEntityCreatedHandler(func(event st.EntityCreatedEvent) {
		teamId := -1
		var clanName string
		var flagImage string
		score := 0

		event.Entity().FindProperty("m_iTeamNum").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
			teamId = ue.Value().IntVal
		})
		event.Entity().FindProperty("m_szClanTeamname").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
			clanName = ue.Value().StringVal
		})
		event.Entity().FindProperty("m_szTeamFlagImage").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
			flagImage = ue.Value().StringVal
		})
		event.Entity().FindProperty("m_scoreTotal").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
			score = ue.Value().IntVal
		})

		event.Entity().FindProperty("m_szTeamname").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
			team := ue.Value().StringVal

			var s *TeamState
			var t common.Team

			switch team {
			case teamName_Ct:
				s = &p.ctState
				t = common.Team_CounterTerrorists

			case teamName_Terrorist:
				s = &p.tState
				t = common.Team_Terrorists

			case teamName_Unassigned: // Ignore
			case teamName_Spectator: // Ignore

			default:
				panic("Unexpected team: " + team)
			}

			if s != nil {
				// Set values that were already updated
				s.id = teamId
				s.clanName = clanName
				s.flag = flagImage
				s.score = score

				// Register direct updates for the future
				event.Entity().FindProperty("m_iTeamNum").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
					s.id = ue.Value().IntVal
				})
				event.Entity().FindProperty("m_szClanTeamname").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
					s.clanName = ue.Value().StringVal
				})
				event.Entity().FindProperty("m_szTeamFlagImage").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
					s.flag = ue.Value().StringVal
				})
				event.Entity().FindProperty("m_scoreTotal").RegisterPropertyUpdateHandler(func(ue st.PropertyUpdateEvent) {
					s.score = ue.Value().IntVal
				})

				// FIXME: This only sets the team at the start. . . We also have a player-specific update handler that changes the team so maybe this is unneccessary?
				if teamId != -1 {
					s.id = teamId
					for _, pl := range p.players {
						if pl != nil && pl.TeamId == teamId {
							pl.Team = t
						}
					}
				}
			}
		})
	})
}

func (p *Parser) handleBombSites() {
	p.stParser.FindServerClassByName("CCSPlayerResource").RegisterEntityCreatedHandler(func(playerResource st.EntityCreatedEvent) {
		playerResource.Entity().FindProperty("m_bombsiteCenterA").RegisterPropertyUpdateHandler(func(center st.PropertyUpdateEvent) {
			p.bombsiteA.center = center.Value().VectorVal
		})
		playerResource.Entity().FindProperty("m_bombsiteCenterB").RegisterPropertyUpdateHandler(func(center st.PropertyUpdateEvent) {
			p.bombsiteB.center = center.Value().VectorVal
		})
	})

	p.stParser.FindServerClassByName("CBaseTrigger").RegisterEntityCreatedHandler(func(baseTrigger st.EntityCreatedEvent) {
		t := &BoundingBoxInformation{index: baseTrigger.Entity().Id}
		p.triggers = append(p.triggers, t)

		baseTrigger.Entity().FindProperty("m_Collision.m_vecMins").RegisterPropertyUpdateHandler(func(vec st.PropertyUpdateEvent) {
			t.min = vec.Value().VectorVal
		})
		baseTrigger.Entity().FindProperty("m_Collision.m_vecMaxs").RegisterPropertyUpdateHandler(func(vec st.PropertyUpdateEvent) {
			t.max = vec.Value().VectorVal
		})
	})
}

func (p *Parser) handlePlayers() {
	p.stParser.FindServerClassByName("CCSPlayer").RegisterEntityCreatedHandler(func(e st.EntityCreatedEvent) {
		p.handleNewPlayer(e.Entity())
	})

	p.stParser.FindServerClassByName("CCSPlayerResource").RegisterEntityCreatedHandler(func(pr st.EntityCreatedEvent) {
		for i := 0; i < maxPlayers; i++ {
			i2 := i // Copy so it stays the same (for passing to handlers)
			iStr := fmt.Sprintf("%03d", i)

			pr.Entity().FindProperty("m_szClan." + iStr).RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
				p.additionalPlayerInfo[i2].ClanTag = e.Value().StringVal
			})

			setIntLazy := func(prop string, setter func(int)) {
				pr.Entity().FindProperty(prop).RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
					setter(e.Value().IntVal)
				})
			}

			setIntLazy("m_iPing."+iStr, func(val int) { p.additionalPlayerInfo[i2].Ping = val })
			setIntLazy("m_iScore."+iStr, func(val int) { p.additionalPlayerInfo[i2].Score = val })
			setIntLazy("m_iKills."+iStr, func(val int) { p.additionalPlayerInfo[i2].Kills = val })
			setIntLazy("m_iDeaths."+iStr, func(val int) { p.additionalPlayerInfo[i2].Deaths = val })
			setIntLazy("m_iAssists."+iStr, func(val int) { p.additionalPlayerInfo[i2].Assists = val })
			setIntLazy("m_iMVPs."+iStr, func(val int) { p.additionalPlayerInfo[i2].MVPs = val })
			setIntLazy("m_iTotalCashSpent."+iStr, func(val int) { p.additionalPlayerInfo[i2].TotalCashSpent = val })
		}
	})
}

func (p *Parser) handleNewPlayer(playerEntity *st.Entity) {
	var pl *common.Player
	if p.players[playerEntity.Id-1] != nil {
		pl = p.players[playerEntity.Id-1]
	} else {
		pl = common.NewPlayer()
		p.players[playerEntity.Id-1] = pl
		pl.SteamId = -1
		pl.Name = "unconnected"
	}

	pl.EntityId = playerEntity.Id
	pl.Entity = playerEntity

	playerEntity.FindProperty("cslocaldata.m_vecOrigin").RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
		pl.Position.X = e.Value().VectorVal.X
		pl.Position.Y = e.Value().VectorVal.Y
	})

	playerEntity.FindProperty("cslocaldata.m_vecOrigin[2]").RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
		pl.Position.Z = e.Value().VectorVal.Z
	})

	playerEntity.FindProperty("m_iTeamNum").RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
		pl.TeamId = e.Value().IntVal

		// FIXME: We could probably just cast TeamId to common.Team or not even set it because the teamIds should be the same. . . needs testing
		switch pl.TeamId {
		case p.ctState.id:
			pl.Team = common.Team_CounterTerrorists
		case p.tState.id:
			pl.Team = common.Team_Terrorists
		default:
			pl.Team = common.Team_Spectators
		}
	})

	// Some helpers because I cant be arsed
	setIntLazy := func(prop string, setter func(int)) {
		playerEntity.FindProperty(prop).RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
			setter(e.Value().IntVal)
		})
	}

	setFloatLazy := func(prop string, setter func(float32)) {
		playerEntity.FindProperty(prop).RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
			setter(e.Value().FloatVal)
		})
	}

	setFloat64Lazy := func(prop string, setter func(float64)) {
		playerEntity.FindProperty(prop).RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
			setter(float64(e.Value().FloatVal))
		})
	}

	setIntLazy("m_iHealth", func(val int) { pl.Hp = val })
	setIntLazy("m_ArmorValue", func(val int) { pl.Armor = val })
	setIntLazy("m_bHasDefuser", func(val int) { pl.HasDefuseKit = val == 1 })
	setIntLazy("m_bHasHelmet", func(val int) { pl.HasHelmet = val == 1 })
	setIntLazy("localdata.m_Local.m_bDucking", func(val int) { pl.IsDucking = val == 1 })
	setIntLazy("m_iAccount", func(val int) { pl.Money = val })

	setFloatLazy("m_angEyeAngles[1]", func(val float32) { pl.ViewDirectionX = val })
	setFloatLazy("m_angEyeAngles[0]", func(val float32) { pl.ViewDirectionY = val })
	setFloatLazy("m_flFlashDuration", func(val float32) { pl.FlashDuration = val })

	setFloat64Lazy("localdata.m_vecVelocity[0]", func(val float64) { pl.Velocity.X = val })
	setFloat64Lazy("localdata.m_vecVelocity[1]", func(val float64) { pl.Velocity.Y = val })
	setFloat64Lazy("localdata.m_vecVelocity[2]", func(val float64) { pl.Velocity.Z = val })

	setIntLazy("m_unCurrentEquipmentValue", func(val int) { pl.CurrentEquipmentValue = val })
	setIntLazy("m_unRoundStartEquipmentValue", func(val int) { pl.RoundStartEquipmentValue = val })
	setIntLazy("m_unFreezetimeEndEquipmentValue", func(val int) { pl.FreezetimeEndEquipmentValue = val })

	wepPrefix := playerWeaponPrePrefix + playerWeaponPrefix

	for _, prop := range playerEntity.Props() {
		if prop.Entry().Name() == playerWeaponPrefix+"000" {
			wepPrefix = playerWeaponPrefix
			break
		}
	}

	var cache [maxWeapons]int

	for i, v := range cache {
		i2 := i // Copy for passing to handler
		playerEntity.FindProperty(wepPrefix + fmt.Sprintf("%03d", i)).RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
			idx := e.Value().IntVal & common.IndexMask
			if idx != common.IndexMask {
				if v != 0 {
					// Player already has a weapon in this slot.
					pl.RawWeapons[cache[i2]] = nil
					cache[i2] = 0
				}
				cache[i2] = idx
				p.attributeWeapon(idx, pl)
			} else {
				if cache[i2] != 0 && pl.RawWeapons[cache[i2]] != nil {
					pl.RawWeapons[cache[i2]].Owner = nil
				}
				pl.RawWeapons[cache[i2]] = nil
			}
			cache[i2] = 0
		})
	}

	setIntLazy("m_hActiveWeapon", func(val int) { pl.ActiveWeaponId = val & common.IndexMask })

	for i := 0; i < 32; i++ {
		i2 := i // Copy so it stays the same
		setIntLazy("m_iAmmo."+fmt.Sprintf("%03d", i2), func(val int) { pl.AmmoLeft[i2] = val })
	}
}

func (p *Parser) attributeWeapon(index int, player *common.Player) {
	wep := &p.weapons[index]
	wep.Owner = player
	player.RawWeapons[index] = wep
}

func (p *Parser) handleWeapons() {
	for i := 0; i < maxEntities; i++ {
		p.weapons[i] = common.NewEquipment("")
	}

	for _, sc := range p.stParser.ServerClasses() {
		for _, bc := range sc.BaseClasses {
			if bc.Name == "CWeaponCSBase" {
				sc.RegisterEntityCreatedHandler(p.handleWeapon)
			}
		}
	}
}

func (p *Parser) handleWeapon(event st.EntityCreatedEvent) {
	eq := p.weapons[event.Entity().Id]
	eq.EntityId = event.Entity().Id
	eq.Weapon = p.equipmentMapping[event.ServerClass()]
	eq.AmmoInMagazine = -1

	event.Entity().FindProperty("m_iClip1").RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
		eq.AmmoInMagazine = e.Value().IntVal - 1
	})

	event.Entity().FindProperty("LocalWeaponData.m_iPrimaryAmmoType").RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
		eq.AmmoType = e.Value().IntVal
	})

	wepFix := func(ok string, change string, changer func()) {
		event.Entity().FindProperty("m_nModelIndex").RegisterPropertyUpdateHandler(func(e st.PropertyUpdateEvent) {
			eq.OriginalString = p.modelPreCache[e.Value().IntVal]
			if strings.Contains(eq.OriginalString, ok) {
				// That's already ok!
			} else if strings.Contains(eq.OriginalString, change) {
				changer()
			} else {
				panic("Unknown weapon model " + eq.OriginalString)
			}
		})
	}

	// FIXME: Deag/R8???
	switch eq.Weapon {
	case common.EE_P2000:
		wepFix("_pist_hkp2000", "_pist_223", func() { eq.Weapon = common.EE_USP })
	case common.EE_M4A4:
		wepFix("_rif_m4a1", "_rif_m4a1_s", func() { eq.Weapon = common.EE_M4A1 })
	case common.EE_P250:
		wepFix("_pist_p250", "_pist_cz_75", func() { eq.Weapon = common.EE_CZ })
	}
}
