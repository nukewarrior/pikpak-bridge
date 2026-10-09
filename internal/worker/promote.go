package worker

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/nukewarrior/pikpak-bridge/internal/domain"
)

// PromoteLocalDownloads handles only explicitly requested repeat downloads.
// All files are staged and verified before any existing NAS path is touched.
// Each replacement is a rename on the same filesystem, with rollback on errors.
func PromoteLocalDownloads(localDir, taskID string, downloads []domain.Download) error {
    if localDir == "" || !filepath.IsAbs(localDir) {
        return errors.New("必须配置 Bridge 可访问的 NAS 本地挂载目录 local_dir")
    }
    if taskID == "" || strings.ContainsAny(taskID, "/\\") {
        return errors.New("invalid task ID")
    }
    base, err := filepath.EvalSymlinks(localDir)
    if err != nil { return fmt.Errorf("NAS 挂载目录不可访问: %w", err) }
    if info, err := os.Stat(base); err != nil || !info.IsDir() {
        return errors.New("NAS 挂载目录不存在或不是目录")
    }
    stage := filepath.Join(base, ".pikpak-bridge-staging", taskID)
    backupDir := filepath.Join(base, ".pikpak-bridge-backups", taskID)
    marker := filepath.Join(stage, ".bridge-committed")
    if _, err := os.Stat(marker); err == nil { return nil }

    type item struct { source, target, backup string; hadOld, moved bool }
    items := make([]item, 0, len(downloads))
    for _, d := range downloads {
        rel := filepath.Clean(filepath.FromSlash(d.RelativePath))
        if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
            return fmt.Errorf("unsafe relative path: %q", d.RelativePath)
        }
        src := filepath.Join(stage, rel)
        dst := filepath.Join(base, rel)
        backup := filepath.Join(backupDir, rel)
        if err := safePath(base, dst); err != nil { return err }
        if err := safePath(stage, src); err != nil { return err }
        fi, err := os.Lstat(src)
        if err != nil { return fmt.Errorf("临时下载文件不存在 %s: %w", rel, err) }
        if !fi.Mode().IsRegular() || fi.Size() != d.TotalLength {
            return fmt.Errorf("临时文件大小/类型不符 %s: got %d, expected %d", rel, fi.Size(), d.TotalLength)
        }
        items = append(items, item{source:src,target:dst,backup:backup})
    }
    undo := func(last int) error {
        var errs []error
        for j:=last;j>=0;j-- {
            m:=items[j]
            if m.moved { if err:=os.Rename(m.target,m.source);err!=nil {errs=append(errs,err)} }
            if m.hadOld { if err:=os.Rename(m.backup,m.target);err!=nil {errs=append(errs,err)} }
        }
        return errors.Join(errs...)
    }
    fail := func(i int, cause error) error {return errors.Join(cause,undo(i))}
    for i:=range items {
        m:=&items[i]
        if err:=os.MkdirAll(filepath.Dir(m.target),0755);err!=nil {return fail(i-1,err)}
        if err:=safePath(base,m.target);err!=nil {return fail(i-1,err)}
        if fi,err:=os.Lstat(m.target);err==nil {
            if !fi.Mode().IsRegular() {return fail(i-1,fmt.Errorf("已有目标不是普通文件: %s",m.target))}
            if err:=os.MkdirAll(filepath.Dir(m.backup),0700);err!=nil {return fail(i-1,err)}
            if _,err:=os.Lstat(m.backup);err==nil {return fail(i-1,fmt.Errorf("备份目录存在残留文件: %s",m.backup))}
            if err:=os.Rename(m.target,m.backup);err!=nil {return fail(i-1,err)}
            m.hadOld=true
        } else if !errors.Is(err,os.ErrNotExist) {return fail(i-1,err)}
        if err:=os.Rename(m.source,m.target);err!=nil {return fail(i,err)}
        m.moved=true
    }
    if err:=os.WriteFile(marker,[]byte("completed"),0600);err!=nil {return fail(len(items)-1,err)}
    // A successful marker makes the operation idempotent after a worker restart.
    _=os.RemoveAll(backupDir)
    return nil
}

// Reject cloud-controlled paths which escape the mount or traverse a symlink.
func safePath(base, target string) error {
    rel,err:=filepath.Rel(base,target)
    if err!=nil || rel==".." || strings.HasPrefix(rel,".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
        return fmt.Errorf("path escapes NAS root: %s",target)
    }
    current:=base
    for _, part:=range strings.Split(rel,string(filepath.Separator)) {
        if part=="" || part=="." {continue}
        current=filepath.Join(current,part)
        fi,err:=os.Lstat(current)
        if errors.Is(err,os.ErrNotExist) {continue}
        if err!=nil {return err}
        if fi.Mode()&os.ModeSymlink!=0 {return fmt.Errorf("refuse symlink path: %s",current)}
    }
    return nil
}
