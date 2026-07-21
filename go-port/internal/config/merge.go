package config

// MergeConfig mirrors utils.merge_config(user, default): recursively fills
// gaps in user from default, with user values always winning on conflict.
// Only defers to default when the corresponding user value doesn't exist,
// and only recurses if BOTH values are maps at that key — matching Python's
// two isinstance(..., dict) guards exactly (a user list always wins over a
// default list/anything else, it is never element-merged).
func MergeConfig(user, def map[string]interface{}) map[string]interface{} {
	if user == nil {
		user = map[string]interface{}{}
	}
	for k, v := range def {
		existing, ok := user[k]
		if !ok {
			user[k] = v
			continue
		}
		existingMap, existingIsMap := existing.(map[string]interface{})
		defMap, defIsMap := v.(map[string]interface{})
		if existingIsMap && defIsMap {
			user[k] = MergeConfig(existingMap, defMap)
		}
		// else: user value wins unchanged, exactly like Python returning
		// `user` untouched when either side isn't a dict.
	}
	return user
}

// KeysToStr mirrors utils.keys_to_str: recursively stringifies map keys
// (used when migrating a legacy YAML config, which may parse int/float keys,
// into a TOML-safe structure). Go's YAML decoder already produces
// map[string]interface{}, so this mostly exists for parity/testing; it still
// handles the []interface{} / map[string]interface{} recursion Python does.
func KeysToStr(data interface{}) interface{} {
	switch v := data.(type) {
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			switch item.(type) {
			case []interface{}, map[string]interface{}:
				out[i] = KeysToStr(item)
			default:
				out[i] = item
			}
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, val := range v {
			switch val.(type) {
			case []interface{}, map[string]interface{}:
				out[k] = KeysToStr(val)
			default:
				out[k] = val
			}
		}
		return out
	default:
		return data
	}
}
