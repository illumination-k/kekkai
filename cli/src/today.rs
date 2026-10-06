// The local date, as `date +%Y-%m-%d` prints it.

pub fn local_date() -> String {
    // SAFETY: time and localtime_r only write the structs passed to them.
    unsafe {
        let t = libc::time(std::ptr::null_mut());
        let mut tm: libc::tm = std::mem::zeroed();
        libc::localtime_r(&t, &mut tm);
        format!(
            "{:04}-{:02}-{:02}",
            tm.tm_year + 1900,
            tm.tm_mon + 1,
            tm.tm_mday
        )
    }
}
