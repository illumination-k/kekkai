// Go-style flags, as ./kek parses them (tool_flags_parse): -name value,
// -name=value, --name; flags come before the arguments. The spec lists the
// flags: "json" a boolean, "run=" a value, "seed#" an integer.
use std::collections::HashMap;

pub struct Parsed {
    vals: HashMap<String, String>,
    pub rest: Vec<String>,
}

impl Parsed {
    pub fn get(&self, name: &str) -> Option<&str> {
        self.vals.get(name).map(|s| s.as_str())
    }

    pub fn bool(&self, name: &str) -> bool {
        self.get(name) == Some("true")
    }
}

/// parse returns the flags and the remaining arguments, or the exit status
/// after printing the usage (`-h`: 0; an error: 2).
pub fn parse(spec: &str, usage: fn(), args: &[String]) -> Result<Parsed, i32> {
    let mut vals = HashMap::new();
    let mut i = 0;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            i += 1;
            break;
        }
        if !a.starts_with('-') || a.len() < 2 {
            break;
        }
        let name = a.strip_prefix('-').unwrap();
        let name = name.strip_prefix('-').unwrap_or(name);
        let (name, val) = match name.split_once('=') {
            Some((n, v)) => (n, Some(v.to_string())),
            None => (name, None),
        };
        if name == "h" || name == "help" {
            usage();
            return Err(0);
        }
        let kind = spec.split_whitespace().find_map(|s| {
            if s == name {
                Some('b')
            } else if s.strip_suffix('=') == Some(name) {
                Some('s')
            } else if s.strip_suffix('#') == Some(name) {
                Some('i')
            } else {
                None
            }
        });
        let fail = |msg: String| {
            eprintln!("{msg}");
            usage();
            Err(2)
        };
        let Some(kind) = kind else {
            return fail(format!("flag provided but not defined: -{name}"));
        };
        let v = if kind == 'b' {
            match val.as_deref().unwrap_or("true") {
                "1" | "t" | "T" | "true" | "TRUE" | "True" => "true".to_string(),
                "0" | "f" | "F" | "false" | "FALSE" | "False" => "false".to_string(),
                v => {
                    return fail(format!(
                        "invalid boolean value \"{v}\" for -{name}: parse error"
                    ));
                }
            }
        } else {
            let v = match val {
                Some(v) => v,
                None => {
                    let Some(v) = args.get(i + 1) else {
                        return fail(format!("flag needs an argument: -{name}"));
                    };
                    i += 1;
                    v.clone()
                }
            };
            if kind == 'i' && !is_int64(&v) {
                return fail(format!(
                    "invalid value \"{v}\" for flag -{name}: parse error"
                ));
            }
            v
        };
        vals.insert(name.to_string(), v);
        i += 1;
    }
    Ok(Parsed {
        vals,
        rest: args[i..].to_vec(),
    })
}

/// is_int64: a decimal integer that fits in 64 bits.
pub fn is_int64(s: &str) -> bool {
    let d = s.strip_prefix(['+', '-']).unwrap_or(s);
    if d.is_empty() || !d.bytes().all(|b| b.is_ascii_digit()) || (d.len() > 1 && d.starts_with('0'))
    {
        return false;
    }
    if s.starts_with('-') {
        format!("-{d}").parse::<i64>().is_ok()
    } else {
        d.parse::<i64>().is_ok()
    }
}

/// is_count: a positive integer of at most 9 digits.
pub fn is_count(s: &str) -> bool {
    !s.is_empty() && s.len() <= 9 && s.bytes().all(|b| b.is_ascii_digit()) && !s.starts_with('0')
}

pub fn ncpu() -> usize {
    std::thread::available_parallelism().map_or(1, |n| n.get())
}

/// A regular expression of -run (an extended regular expression in
/// ./kek, grep -E).
pub fn run_regex(re: &str) -> Option<regex::Regex> {
    regex::Regex::new(re).ok()
}

/// select keeps the lines of `tests` that match -run (all without one).
/// None: an invalid expression.
pub fn select(tests: &str, run: &str) -> Option<Vec<String>> {
    let lines = tests.lines().map(|l| l.to_string());
    if run.is_empty() {
        return Some(lines.collect());
    }
    let re = run_regex(run)?;
    Some(lines.filter(|l| re.is_match(l)).collect())
}
